package store

import (
	"bytes"
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

func ev(kind, lane string) kernel.Event {
	return kernel.Event{Kind: kind, Lane: lane, Session: "s1", At: t0}
}

func TestLog_AppendIsOneLinePerEvent(t *testing.T) {
	s, dir, _ := open(t)
	for _, k := range []string{kernel.EvEdit, kernel.EvCommit, kernel.EvMerge} {
		if _, err := s.Append(ev(k, "lane/a")); err != nil {
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
	s.Append(ev("edit", "l"), ev("commit", "l"), ev("merge", "l"))
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
	s.Append(ev("edit", "l"))
	s2, err := Open(dir, fc)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s2.Append(ev("commit", "l"))
	if err != nil || out[0].Seq != 2 {
		t.Fatalf("second handle Append = %+v, %v", out, err)
	}
}

func TestLog_ConcurrentAppendsLoseNothing(t *testing.T) {
	s, dir, _ := open(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if _, err := s.Append(ev("edit", "lane/c")); err != nil {
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
	if n := bytes.Count(raw, []byte("\n")); n != 800 {
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
		batch[i] = kernel.Event{Kind: "edit", Lane: "lane/long", Session: "s", At: t0,
			Detail: map[string]string{"pad": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}}
	}
	if _, err := s.Append(batch...); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "events.jsonl")); fi.Size() <= tailChunk {
		t.Fatalf("log is %d bytes, need more than the %d-byte tail chunk", fi.Size(), tailChunk)
	}
	s2, _ := Open(dir, fc)
	out, err := s2.Append(ev("commit", "lane/long"))
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
	f.Close()

	out, err := s.Append(ev("commit", "l"))
	if err != nil || len(out) != 1 || out[0].Seq != 2 {
		t.Fatalf("Append after a torn tail = %+v, %v; want Seq 2", out, err)
	}
	got, err := s.Read(0)
	if err != nil || len(got) != 2 || got[0].Kind != "edit" || got[0].Seq != 1 || got[1].Kind != "commit" || got[1].Seq != 2 {
		t.Fatalf("Read(0) = %+v, %v; want edit 1 and commit 2", got, err)
	}
}
