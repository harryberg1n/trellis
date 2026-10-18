package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func open(t *testing.T) (*Store, string, *clock.FakeClock) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo-state")
	fc := clock.Fake(t0)
	s, err := Open(dir, fc)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir, fc
}

func ev(kind, tree string) kernel.Event {
	return kernel.Event{Kind: kind, Tree: tree, Session: "s1", At: t0}
}

func TestLog_AppendIsOneLinePerEvent(t *testing.T) {
	s, dir, _ := open(t)
	for _, k := range []string{kernel.EvEdit, kernel.EvCommit, kernel.EvMerge} {
		if _, err := s.Append(ev(k, "tree/a")); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(raw, []byte("\n")); n != 3 {
		t.Fatalf("%d lines, want 3:\n%s", n, raw)
	}
	if n := s.Skipped(); n != 0 {
		t.Fatalf("Skipped() = %d on a clean log, want 0", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "events.quarantine")); err == nil {
		t.Fatal("a clean log made a quarantine file")
	}
	got, err := s.Read(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("Read returned %d events, want 3", len(got))
	}
	for i, want := range []uint64{1, 2, 3} {
		if got[i].Seq != want {
			t.Errorf("event %d Seq = %d, want %d", i, got[i].Seq, want)
		}
	}
	if got[0].Kind != "edit" || got[1].Kind != "commit" || got[2].Kind != "merge" {
		t.Errorf("kinds = %s %s %s", got[0].Kind, got[1].Kind, got[2].Kind)
	}
}

func TestLog_AppendReturnsEventsWithSeq(t *testing.T) {
	s, _, _ := open(t)
	out, err := s.Append(ev("edit", "l"), ev("commit", "l"))
	if err != nil || len(out) != 2 || out[0].Seq != 1 || out[1].Seq != 2 {
		t.Fatalf("Append = %+v, %v", out, err)
	}
}

func TestLog_ReadFromSkipsEarlierEvents(t *testing.T) {
	s, _, _ := open(t)
	if _, err := s.Append(ev("edit", "l"), ev("commit", "l"), ev("merge", "l")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(2)
	if err != nil || len(got) != 2 || got[0].Seq != 2 || got[1].Seq != 3 {
		t.Fatalf("Read(2) = %+v, %v", got, err)
	}
}

func TestLog_ReadWithNoLogIsEmpty(t *testing.T) {
	s, _, _ := open(t)
	got, err := s.Read(1)
	if err != nil || len(got) != 0 {
		t.Fatalf("Read = %+v, %v", got, err)
	}
}

func TestLog_SecondHandleContinuesTheSequence(t *testing.T) {
	s, dir, fc := open(t)
	if _, err := s.Append(ev("edit", "l")); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir, fc)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s2.Append(ev("commit", "l"))
	if err != nil || out[0].Seq != 2 {
		t.Fatalf("second handle Append = %+v, %v", out, err)
	}
}

// spinClock polls without waiting: After is already fired, and every Now call
// moves time on by a fixed step, so the 2 s lock deadline is reachable. A lock
// that never frees ends in a parked event and a failed test, not a hang. It
// belongs to one Store, which calls it under its mutex.
type spinClock struct{ now time.Time }

const spinStep = 10 * time.Microsecond

func (c *spinClock) Now() time.Time {
	c.now = c.now.Add(spinStep)
	return c.now
}

func (c *spinClock) After(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

func TestLog_ConcurrentAppendsLoseNothing(t *testing.T) {
	s, dir, _ := open(t)
	// One Store per goroutine, so only the file lock keeps them apart.
	stores := make([]*Store, 8)
	for g := range stores {
		var err error
		if stores[g], err = Open(dir, &spinClock{now: t0}, WithLockTimeout(10*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for _, gs := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if _, err := gs.Append(ev("edit", "tree/c")); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if n := bytes.Count(raw, []byte{newline}); n != 800 {
		t.Fatalf("%d lines, want 800", n)
	}
	got, err := s.Read(1)
	if err != nil || len(got) != 800 {
		t.Fatalf("Read = %d events, %v", len(got), err)
	}
	seen := map[uint64]bool{}
	for _, e := range got {
		if e.Seq < 1 || e.Seq > 800 || seen[e.Seq] {
			t.Fatalf("bad or repeated Seq %d", e.Seq)
		}
		seen[e.Seq] = true
	}
}

func TestLog_LastSeqComesFromALongLog(t *testing.T) {
	s, dir, fc := open(t)
	batch := make([]kernel.Event, 600)
	for i := range batch {
		batch[i] = kernel.Event{
			Kind: "edit", Tree: "tree/long", Session: "s", At: t0,
			Detail: map[string]string{"pad": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
		}
	}
	if _, err := s.Append(batch...); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "events.jsonl")); fi.Size() <= tailChunk {
		t.Fatalf("log is %d bytes, need more than the %d-byte tail chunk", fi.Size(), tailChunk)
	}
	s2, _ := Open(dir, fc)
	out, err := s2.Append(ev("commit", "tree/long"))
	if err != nil || out[0].Seq != 601 {
		t.Fatalf("Append = %+v, %v; want Seq 601", out, err)
	}
}

func TestLog_AppendOnTimeoutParksAndReports(t *testing.T) {
	s, dir, fc := open(t)
	release, err := s.Hold()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	type res struct {
		out []kernel.Event
		err error
	}
	done := make(chan res, 1)
	go func() {
		out, err := s.Append(ev("edit", "l"))
		done <- res{out, err}
	}()
	fc.BlockUntil(1)
	fc.Advance(2 * time.Second)
	r := <-done
	if !errors.Is(r.err, ErrParked) {
		t.Fatalf("err = %v, want ErrParked", r.err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "events.pending", "*.json"))
	if len(files) != 1 {
		t.Fatalf("%d pending files, want 1", len(files))
	}
	release()
	out, err := s.Append(ev("commit", "l"))
	if err != nil || len(out) != 1 || out[0].Seq != 2 {
		t.Fatalf("Append after release = %+v, %v; want Seq 2", out, err)
	}
	got, _ := s.Read(1)
	if len(got) != 2 || got[0].Kind != "edit" || got[0].Seq != 1 || got[1].Kind != "commit" {
		t.Fatalf("log = %+v", got)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "events.pending", "*.json")); len(files) != 0 {
		t.Fatalf("%d pending files left, want 0", len(files))
	}
}

func TestLog_TornTailDoesNotWedgeTheStore(t *testing.T) {
	s, dir, _ := open(t)
	if _, err := s.Append(ev("edit", "l")); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":2,"kind":"com`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	out, err := s.Append(ev("commit", "l"))
	if err != nil || len(out) != 1 || out[0].Seq != 2 {
		t.Fatalf("Append after a torn tail = %+v, %v; want Seq 2", out, err)
	}
	got, err := s.Read(0)
	if err != nil || len(got) != 2 || got[0].Kind != "edit" || got[0].Seq != 1 || got[1].Kind != "commit" || got[1].Seq != 2 {
		t.Fatalf("Read(0) = %+v, %v; want edit 1 and commit 2", got, err)
	}
	if n := s.Skipped(); n != 1 {
		t.Fatalf("Skipped() = %d, want 1", n)
	}
	q, err := os.ReadFile(filepath.Join(dir, "events.quarantine"))
	if err != nil || string(q) != "{\"seq\":2,\"kind\":\"com"+string(rune(10)) {
		t.Fatalf("events.quarantine = %q, %v; want the fragment and a newline", q, err)
	}
}

func TestFold_ParkedEventIsNeverDuplicated(t *testing.T) {
	s, dir, _ := open(t)
	if err := s.Park(ev("edit", "tree/d")); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "events.pending", "*.json"))
	if len(files) != 1 {
		t.Fatalf("%d pending files, want 1", len(files))
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var parked kernel.Event
	if err := json.Unmarshal(raw, &parked); err != nil {
		t.Fatal(err)
	}
	if parked.Detail["park_id"] == "" {
		t.Fatalf("parked event has no park_id: %s", raw)
	}
	// A fold that crashed after the append, before removing the file.
	err = s.Locked(func(tx *Tx) error {
		tx.folded = true
		_, err := tx.appendRaw([]kernel.Event{parked})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ev("commit", "tree/d")); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Read(1)
	if len(got) != 2 || got[0].Kind != "edit" || got[1].Kind != "commit" || got[1].Seq != 2 {
		t.Fatalf("log = %+v; want edit once, then commit with Seq 2", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "events.pending", "*.json")); len(left) != 0 {
		t.Fatalf("%d pending files left", len(left))
	}
}

func TestLog_ReadFromTouchesOnlyTheTail(t *testing.T) {
	s, dir, fc := open(t)
	batch := make([]kernel.Event, 10000)
	for i := range batch {
		batch[i] = ev("edit", "tree/big")
	}
	if _, err := s.Append(batch...); err != nil {
		t.Fatal(err)
	}
	fresh, err := Open(dir, fc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fresh.Read(9998)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Seq != 9998 || got[1].Seq != 9999 || got[2].Seq != 10000 {
		t.Fatalf("Read(9998) = %+v; want Seq 9998, 9999, 10000", got)
	}
	if n := fresh.BytesRead(); n <= 0 || n >= 64<<10 {
		t.Fatalf("read %d bytes of a log of 10000 lines; want under 65536", n)
	}
}

func TestLog_ReadFromPastTheEndIsEmpty(t *testing.T) {
	s, _, _ := open(t)
	if _, err := s.Append(ev("edit", "l"), ev("edit", "l")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(3)
	if err != nil || len(got) != 0 {
		t.Fatalf("Read(3) = %+v, %v; want none", got, err)
	}
}

func TestLog_UnterminatedCompleteTailKeepsItsSeq(t *testing.T) {
	s, dir, _ := open(t)
	if _, err := s.Append(ev("edit", "l"), ev("edit", "l")); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":3,"kind":"merge","tree":"l","session":"s1","at":"2026-10-18T12:00:00Z"}`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	out, err := s.Append(ev("commit", "l"))
	if err != nil || len(out) != 1 || out[0].Seq != 4 {
		t.Fatalf("Append = %+v, %v; want Seq 4", out, err)
	}
	got, err := s.Read(0)
	if err != nil || len(got) != 4 {
		t.Fatalf("Read(0) = %+v, %v; want 4 events", got, err)
	}
	for i, k := range []string{"edit", "edit", "merge", "commit"} {
		if got[i].Seq != uint64(i+1) || got[i].Kind != k {
			t.Errorf("event %d = Seq %d %s, want Seq %d %s", i, got[i].Seq, got[i].Kind, i+1, k)
		}
	}
}
