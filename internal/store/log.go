package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
)

// ErrParked means the event could not take the lock in time and was written
// to events.pending, to be folded in at the next lock. It is not lost.
var ErrParked = errors.New("store: event parked in events.pending")

// tailChunk is how much of the log end is read to find the last Seq.
const tailChunk = 4 << 10

const newline = byte(10)

// parkIDKey is the Detail key that carries a parked event's file id.
const parkIDKey = "park_id"

// Log is the append-only event log.
type Log interface {
	Append(evs ...kernel.Event) ([]kernel.Event, error)
	Read(from uint64) ([]kernel.Event, error)
}

// Store is one repo's state directory. It implements Log and the checkpoint.
// Shares is the experiment -> treatment percent map the engine resolves new
// trees against; the caller sets it before use.
type Store struct {
	Shares map[string]int

	dir         string
	clock       clock.Clock
	bytesRead   atomic.Int64
	skipMu      sync.Mutex
	skipped     map[int64]bool
	mu          sync.Mutex // in-process; the file lock covers other processes
	rename      func(oldpath, newpath string) error
	remove      func(name string) error
	fsync       func(*os.File) error
	lockTimeout time.Duration
}

var _ Log = (*Store)(nil)

// Option configures Open.
type Option func(*Store)

// WithLockTimeout sets how long an operation waits for the repo lock before
// it parks its event. The default is LockTimeout.
func WithLockTimeout(d time.Duration) Option {
	return func(s *Store) { s.lockTimeout = d }
}

// Open prepares repoDir and returns its Store.
func Open(repoDir string, c clock.Clock, opts ...Option) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(repoDir, "events.pending"), dirMode); err != nil {
		return nil, err
	}
	s := &Store{dir: repoDir, clock: c, rename: os.Rename, remove: os.Remove, fsync: (*os.File).Sync, skipped: map[int64]bool{}, lockTimeout: LockTimeout}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

func (s *Store) logPath() string    { return filepath.Join(s.dir, "events.jsonl") }
func (s *Store) pendingDir() string { return filepath.Join(s.dir, "events.pending") }

// Tx is the work done while the lock is held.
type Tx struct {
	s      *Store
	next   uint64
	tail   uint64 // the log's last Seq when the lock was taken
	last   uint64
	folded bool
	mode   Durability
}

// Locked runs fn holding the in-process mutex and the file lock. It returns
// ErrLockTimeout, without running fn, if the file lock is not free in time.
func (s *Store) Locked(fn func(*Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.acquire()
	if err != nil {
		return err
	}
	defer release()
	last, err := s.lastSeq()
	if err != nil {
		return err
	}
	return fn(&Tx{s: s, next: last + 1, tail: last})
}

// Append takes the lock, folds in parked events, then appends evs with fresh
// Seq values. On a lock timeout it parks evs and returns ErrParked.
func (s *Store) Append(evs ...kernel.Event) ([]kernel.Event, error) {
	var out []kernel.Event
	err := s.Locked(func(tx *Tx) error {
		var err error
		out, err = tx.Append(evs...)
		return err
	})
	if errors.Is(err, ErrLockTimeout) {
		for _, ev := range evs {
			if perr := s.Park(ev); perr != nil {
				return nil, perr
			}
		}
		return evs, ErrParked
	}
	return out, err
}

// FoldPending appends the parked events, oldest first, each passed through
// stamp (nil leaves it as is), and removes their files. It runs once per Tx;
// Append calls it with a nil stamp if the caller has not.
func (tx *Tx) FoldPending(stamp func(kernel.Event) kernel.Event) ([]kernel.Event, error) {
	if tx.folded {
		return nil, nil
	}
	tx.folded = true
	return tx.foldDir(tx.s.pendingDir(), stamp, 0)
}

// FoldFallback appends the events parked in dir/events.pending, a fallback
// store another process wrote when this one was unusable, oldest first, and
// removes their files. At most limit files are taken (0 means all). A dir that
// is not there folds nothing.
func (tx *Tx) FoldFallback(dir string, limit int) ([]kernel.Event, error) {
	return tx.foldDir(filepath.Join(dir, "events.pending"), nil, limit)
}

type parked struct {
	name string
	ev   kernel.Event
}

// readParked reads the *.json files of dir. An unreadable one is set aside as
// .bad. A dir that is not there holds nothing.
func readParked(dir string) ([]parked, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ps []parked
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(name)
		var ev kernel.Event
		if err == nil {
			err = decodeEvent(raw, &ev)
		}
		if err != nil {
			// Keep it for a human; never wedge the lock. If the rename fails the
			// file is simply tried and set aside again on the next fold.
			_ = os.Rename(name, name+".bad")
			continue
		}
		ps = append(ps, parked{name, ev})
	}
	return ps, nil
}

func (tx *Tx) foldDir(dir string, stamp func(kernel.Event) kernel.Event, limit int) ([]kernel.Event, error) {
	ps, err := readParked(dir)
	if err != nil {
		return nil, err
	}
	// A fold that crashed after its append left the file behind; its event is
	// already in the log, so drop the file instead of appending it again.
	if len(ps) > 0 {
		logged, err := tx.s.Read(1)
		if err != nil {
			return nil, err
		}
		have := map[string]bool{}
		for _, e := range logged {
			if id := e.Detail[parkIDKey]; id != "" {
				have[id] = true
			}
		}
		kept := ps[:0]
		for _, p := range ps {
			if id := p.ev.Detail[parkIDKey]; id != "" && have[id] {
				// Best effort: a file another process holds open (an indexer, a
				// scanner) must not wedge every append. The park-id dedupe drops
				// the leftover on a later fold; it is not re-appended now.
				_ = tx.s.remove(p.name)
				continue
			}
			kept = append(kept, p)
		}
		ps = kept
	}
	sort.SliceStable(ps, func(i, j int) bool {
		if !ps[i].ev.At.Equal(ps[j].ev.At) {
			return ps[i].ev.At.Before(ps[j].ev.At)
		}
		return ps[i].name < ps[j].name
	})
	if limit > 0 && len(ps) > limit {
		ps = ps[:limit]
	}
	evs := make([]kernel.Event, len(ps))
	for i, p := range ps {
		evs[i] = p.ev
		if stamp != nil {
			evs[i] = stamp(p.ev)
		}
	}
	out, err := tx.appendRaw(evs)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		// The events are in the log: a leftover file is dropped by the dedupe
		// above on the next fold, so failing the Tx here would only mislead.
		_ = tx.s.remove(p.name)
	}
	return out, nil
}

// CapParked keeps at most limit files in events.pending and removes the oldest
// (by modification time, then name) beyond that.
func (s *Store) CapParked(limit int) error {
	entries, err := os.ReadDir(s.pendingDir())
	if err != nil {
		return err
	}
	type file struct {
		name string
		mod  time.Time
	}
	var files []file
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, file{e.Name(), info.ModTime()})
		}
	}
	if len(files) <= limit {
		return nil
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].mod.Equal(files[j].mod) {
			return files[i].mod.Before(files[j].mod)
		}
		return files[i].name < files[j].name
	})
	var errs []error
	for _, f := range files[:len(files)-limit] {
		if err := s.remove(filepath.Join(s.pendingDir(), f.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Append folds in parked events (once), then appends evs.
func (tx *Tx) Append(evs ...kernel.Event) ([]kernel.Event, error) {
	if _, err := tx.FoldPending(nil); err != nil {
		return nil, err
	}
	return tx.appendRaw(evs)
}

// Tail is the log's last Seq at the moment the lock was taken.
func (tx *Tx) Tail() uint64 { return tx.tail }

// LastSeq is the highest Seq this Tx has appended, or 0.
func (tx *Tx) LastSeq() uint64 { return tx.last }

func (tx *Tx) appendRaw(evs []kernel.Event) ([]kernel.Event, error) {
	if len(evs) == 0 {
		return nil, nil
	}
	out := make([]kernel.Event, len(evs))
	var buf bytes.Buffer
	for i, ev := range evs {
		ev.Seq = tx.next + uint64(i)
		line, err := json.Marshal(ev)
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
		out[i] = ev
	}
	f, err := os.OpenFile(tx.s.logPath(), os.O_APPEND|os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // also on a panic in fsync; the explicit Close below reports its error
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err == nil && last[0] != newline {
			// a torn tail: keep it as its own junk line, not fused to ours
			buf = *bytes.NewBuffer(append([]byte{newline}, buf.Bytes()...))
		}
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		return nil, err
	}
	if tx.mode == Durable {
		if err := tx.s.fsync(f); err != nil {
			return nil, err
		}
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	tx.next += uint64(len(evs))
	tx.last = out[len(out)-1].Seq
	return out, nil
}

// Park writes ev to events.pending/<uuid>.json without the lock. The file is
// renamed into place, so a folder never reads half of it.
func (s *Store) Park(ev kernel.Event) error {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	name := hex.EncodeToString(id[:])
	d := maps.Clone(ev.Detail)
	if d == nil {
		d = make(map[string]string, 1)
	}
	d[parkIDKey] = name // lets a fold tell a parked event it already appended
	ev.Detail = d
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	// Synced through the file's own Sync, not the store's seam: a parked event
	// is never a Lazy write.
	return WriteAtomic(s.pendingDir(), name+".json", raw,
		WithSync((*os.File).Sync), WithMode(fileMode))
}

// BytesRead is how many log bytes this Store has read, for tests that pin
// the cost of a read.
func (s *Store) BytesRead() int64 { return s.bytesRead.Load() }

// Read returns the events with Seq >= from, in order, reading backwards from
// the end of the log so its cost follows the events returned, not the log
// size. Seq is increasing, so the scan stops at the first line below from.
func (s *Store) Read(from uint64) ([]kernel.Event, error) {
	evs, _, err := s.scanTail(from, false)
	return evs, err
}

// lastSeq reads the end of the log for the highest Seq; 0 for no log.
func (s *Store) lastSeq() (uint64, error) {
	_, last, err := s.scanTail(math.MaxUint64, true)
	return last, err
}

// Skipped is how many unparsable log lines this Store has set aside in
// events.quarantine since Open.
func (s *Store) Skipped() int {
	s.skipMu.Lock()
	defer s.skipMu.Unlock()
	return len(s.skipped)
}

// skip appends a line that does not parse to events.quarantine, once per
// offset. The claim on an offset is a file made with O_EXCL in
// events.quarantine.at: creating it is the check and the claim in one step, so
// two processes reading the same junk line cannot both set it aside, with no
// lock to take (the caller may hold it already). A line with no offset in the
// log (off < 0: a corrupt checkpoint) is kept once per Store. Failing to write
// it must not fail the read.
func (s *Store) skip(l logLine) {
	s.skipMu.Lock()
	defer s.skipMu.Unlock()
	if s.skipped[l.off] {
		return
	}
	s.skipped[l.off] = true
	if l.off >= 0 && !s.claimQuarantine(l.off) {
		return
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "events.quarantine"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return
	}
	// Best effort, as documented above: a failed write or close only means the
	// line is not quarantined, and the read must not fail for it.
	_, _ = f.Write(append(append([]byte(nil), l.raw...), newline))
	_ = f.Close()
}

// claimQuarantine reports whether this call is the first, in any process, to
// claim the log offset. A directory that cannot be made claims nothing, and
// the line is then set aside once per Store, as for a checkpoint.
func (s *Store) claimQuarantine(off int64) bool {
	dir := filepath.Join(s.dir, "events.quarantine.at")
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return true
	}
	f, err := os.OpenFile(filepath.Join(dir, strconv.FormatInt(off, 10)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return !errors.Is(err, os.ErrExist)
	}
	_ = f.Close()
	return true
}

type logLine struct {
	off        int64 // offset of the line in the log
	terminated bool
	raw        []byte
	ev         kernel.Event
	ok         bool
}

// splitLines cuts buf into lines. A line counts if it parses, newline or not:
// an unterminated tail that parses is a complete event missing its newline,
// and one that does not is a torn append.
func splitLines(buf []byte, base int64) []logLine {
	var out []logLine
	pos := base
	for len(buf) > 0 {
		i := bytes.IndexByte(buf, newline)
		terminated := i >= 0
		raw := buf
		adv := int64(len(buf))
		if terminated {
			raw, buf = buf[:i], buf[i+1:]
			adv = int64(i) + 1
		} else {
			buf = nil
		}
		off := pos
		pos += adv
		if len(raw) == 0 {
			continue
		}
		l := logLine{raw: raw, off: off, terminated: terminated}
		l.ok = decodeEvent(raw, &l.ev) == nil
		out = append(out, l)
	}
	return out
}

// scanTail reads the log backwards in doubling chunks until a line with
// Seq < from, and returns the events with Seq >= from and the highest Seq seen.
func (s *Store) scanTail(from uint64, locked bool) ([]kernel.Event, uint64, error) {
	f, err := os.Open(s.logPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }() // read-only
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	var buf []byte
	start := fi.Size()
	for chunk := int64(tailChunk); ; chunk *= 2 {
		n := min(chunk, start)
		piece := make([]byte, n)
		if _, err := f.ReadAt(piece, start-n); err != nil && !errors.Is(err, io.EOF) {
			return nil, 0, err
		}
		s.bytesRead.Add(n)
		start -= n
		buf = append(piece, buf...)
		lines := splitLines(buf, start)
		if start > 0 && len(lines) > 0 {
			lines = lines[1:] // the first line may be cut by the chunk start
		}
		cut := -1
		for i := len(lines) - 1; i >= 0; i-- {
			if lines[i].ok && lines[i].ev.Seq < from {
				cut = i
				break
			}
		}
		if cut < 0 && start > 0 {
			continue
		}
		var evs []kernel.Event
		var last uint64
		for i := range lines {
			l := &lines[i]
			if !l.ok {
				// An unterminated fragment may be a write in flight unless we
				// hold the lock; anything else unparsable is junk to keep.
				if i > cut && (l.terminated || locked) {
					s.skip(*l)
				}
				continue
			}
			last = l.ev.Seq
			if i > cut && l.ev.Seq >= from {
				evs = append(evs, l.ev)
			}
		}
		return evs, last, nil
	}
}
