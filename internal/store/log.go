package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
)

// ErrParked means the event could not take the lock in time and was written
// to events.pending, to be folded in at the next lock. It is not lost.
var ErrParked = errors.New("store: event parked in events.pending")

// tailChunk is how much of the log end is read to find the last Seq.
const tailChunk = 64 << 10

const newline = byte(10)

// Log is the append-only event log.
type Log interface {
	Append(evs ...kernel.Event) ([]kernel.Event, error)
	Read(from uint64) ([]kernel.Event, error)
}

// Store is one repo's state directory. It implements Log and the checkpoint.
// Shares is the experiment -> treatment percent map the engine resolves new
// lanes against; the caller sets it before use.
type Store struct {
	Shares map[string]int

	dir    string
	clock  clock.Clock
	mu     sync.Mutex // in-process; the file lock covers other processes
	rename func(oldpath, newpath string) error
}

var _ Log = (*Store)(nil)

// Open prepares repoDir and returns its Store.
func Open(repoDir string, c clock.Clock) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(repoDir, "events.pending"), 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: repoDir, clock: c, rename: os.Rename}, nil
}

func (s *Store) logPath() string    { return filepath.Join(s.dir, "events.jsonl") }
func (s *Store) pendingDir() string { return filepath.Join(s.dir, "events.pending") }

// Tx is the work done while the lock is held.
type Tx struct {
	s      *Store
	next   uint64
	last   uint64
	folded bool
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
	return fn(&Tx{s: s, next: last + 1})
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
	files, err := filepath.Glob(filepath.Join(tx.s.pendingDir(), "*.json"))
	if err != nil {
		return nil, err
	}
	type parked struct {
		name string
		ev   kernel.Event
	}
	var ps []parked
	for _, name := range files {
		raw, err := os.ReadFile(name)
		var ev kernel.Event
		if err == nil {
			err = json.Unmarshal(raw, &ev)
		}
		if err != nil {
			os.Rename(name, name+".bad") // keep it for a human; never wedge the lock
			continue
		}
		ps = append(ps, parked{name, ev})
	}
	sort.SliceStable(ps, func(i, j int) bool {
		if !ps[i].ev.At.Equal(ps[j].ev.At) {
			return ps[i].ev.At.Before(ps[j].ev.At)
		}
		return ps[i].name < ps[j].name
	})
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
		os.Remove(p.name)
	}
	return out, nil
}

// Append folds in parked events (once), then appends evs.
func (tx *Tx) Append(evs ...kernel.Event) ([]kernel.Event, error) {
	if _, err := tx.FoldPending(nil); err != nil {
		return nil, err
	}
	return tx.appendRaw(evs)
}

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
	f, err := os.OpenFile(tx.s.logPath(), os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err == nil && last[0] != newline {
			// a torn tail: keep it as its own junk line, not fused to ours
			buf = *bytes.NewBuffer(append([]byte{newline}, buf.Bytes()...))
		}
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
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
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	final := filepath.Join(s.pendingDir(), hex.EncodeToString(id[:])+".json")
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// Read returns the events with Seq >= from, in order. A final line with no
// newline (a write in flight) is ignored.
func (s *Store) Read(from uint64) ([]kernel.Event, error) {
	raw, err := os.ReadFile(s.logPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []kernel.Event
	for len(raw) > 0 {
		i := bytes.IndexByte(raw, '\n')
		if i < 0 {
			break
		}
		line := raw[:i]
		raw = raw[i+1:]
		var ev kernel.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // junk left by a torn append
		}
		if ev.Seq >= from {
			out = append(out, ev)
		}
	}
	return out, nil
}

// lastSeq reads the end of the log for the highest Seq; 0 for no log. Only
// newline-terminated lines count: an unterminated tail is a torn append, and a
// terminated line that does not parse is junk left by one. Both are skipped.
func (s *Store) lastSeq() (uint64, error) {
	f, err := os.Open(s.logPath())
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	for chunk := int64(tailChunk); ; chunk *= 2 {
		start := fi.Size() - chunk
		if start < 0 {
			start = 0
		}
		buf := make([]byte, fi.Size()-start)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		end := bytes.LastIndexByte(buf, newline)
		if end < 0 {
			if start == 0 {
				return 0, nil
			}
			continue
		}
		lines := bytes.Split(buf[:end], []byte{newline})
		if start > 0 {
			lines = lines[1:] // the first line may be cut by the chunk start
		}
		for i := len(lines) - 1; i >= 0; i-- {
			var ev kernel.Event
			if json.Unmarshal(lines[i], &ev) == nil {
				return ev.Seq, nil
			}
		}
		if start == 0 {
			return 0, nil
		}
	}
}
