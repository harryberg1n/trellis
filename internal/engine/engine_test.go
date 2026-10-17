package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/store"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func open(t *testing.T) (*store.Store, string, *clock.FakeClock) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo-state")
	fc := clock.Fake(t0)
	s, err := store.Open(dir, fc)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir, fc
}

func ev(kind, lane string) kernel.Event {
	return kernel.Event{Kind: kind, Lane: lane, Session: "s1", At: t0}
}

func TestEngine_StampsArmsFromStickyBeforeAppend(t *testing.T) {
	s, _, _ := open(t)
	s.Shares = map[string]int{"exp1": 100, "exp2": 0}
	if _, err := Step(s, nil, ev(kernel.EvEdit, "lane/a"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	// A share change after the lane's first event must not move it.
	s.Shares = map[string]int{"exp1": 0, "exp2": 100}
	if _, err := Step(s, nil, ev(kernel.EvCommit, "lane/a"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(1)
	if err != nil || len(got) != 2 {
		t.Fatalf("Read = %+v, %v", got, err)
	}
	want := map[string]string{"exp1": "treatment", "exp2": "control"}
	for i, e := range got {
		if !reflect.DeepEqual(e.Arms, want) {
			t.Errorf("event %d Arms = %v, want %v", i+1, e.Arms, want)
		}
	}
}

func TestEngine_CallerArmsAreRecordedAndKept(t *testing.T) {
	s, _, _ := open(t)
	first := ev(kernel.EvEdit, "lane/b")
	first.Arms = map[string]string{"exp1": "control"}
	Step(s, nil, first, kernel.Context{})
	s.Shares = map[string]int{"exp1": 100}
	Step(s, nil, ev(kernel.EvEdit, "lane/b"), kernel.Context{})
	got, _ := s.Read(1)
	if got[1].Arms["exp1"] != "control" {
		t.Fatalf("second event Arms = %v, want exp1=control", got[1].Arms)
	}
}

func TestEngine_FlagsEventsAfterMergeAndPersistsLaneState(t *testing.T) {
	s, _, _ := open(t)
	Step(s, nil, ev(kernel.EvEdit, "lane/m"), kernel.Context{})
	Step(s, nil, ev(kernel.EvLaneMerged, "lane/m"), kernel.Context{})
	Step(s, nil, ev(kernel.EvEdit, "lane/m"), kernel.Context{})
	got, _ := s.Read(1)
	if got[2].Detail["after"] != "merged" {
		t.Fatalf("third event Detail = %v, want after=merged", got[2].Detail)
	}
	if got[0].Detail["after"] != "" {
		t.Fatalf("first event wrongly flagged: %v", got[0].Detail)
	}
}

func TestEngine_ReturnsDecideActions(t *testing.T) {
	s, _, _ := open(t)
	rows := []kernel.Row{{ID: "r1", State: kernel.Admitted}}
	acts, err := Step(s, rows, ev(kernel.EvEdit, "lane/a"), kernel.Context{})
	if err != nil || len(acts) != 0 {
		t.Fatalf("Step = %v, %v; the lane-2 matcher matches nothing", acts, err)
	}
}

func TestEngine_CheckpointCarriesLastSeq(t *testing.T) {
	s, dir, _ := open(t)
	Step(s, nil, ev(kernel.EvEdit, "lane/a"), kernel.Context{})
	Step(s, nil, ev(kernel.EvEdit, "lane/a"), kernel.Context{})
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"last_seq":2,"arms":{"lane/a":{}},"lanes":{"lane/a":1}}`
	if string(raw) != want {
		t.Fatalf("state.json = %s, want %s", raw, want)
	}
}

func TestLock_TimeoutParksTheEventInsteadOfDropping(t *testing.T) {
	s, dir, fc := open(t)
	release, err := s.Hold()
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	done := make(chan error, 1)
	go func() {
		_, err := Step(s, nil, ev(kernel.EvEdit, "lane/p"), kernel.Context{})
		done <- err
	}()
	fc.BlockUntil(1) // Step is waiting on the lock, on the fake clock
	fc.Advance(store.LockTimeout)
	if err := <-done; err != nil {
		t.Fatalf("Step = %v, want nil: a parked event is not a failure", err)
	}
	if got := fc.Now().Sub(t0); got != 2*time.Second {
		t.Fatalf("fake time elapsed = %v, want 2s", got)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "events.pending", "*.json"))
	if len(files) != 1 {
		t.Fatalf("%d pending files, want 1", len(files))
	}
	if got, _ := s.Read(1); len(got) != 0 {
		t.Fatalf("log has %d events while the lock was held", len(got))
	}

	release()
	if _, err := Step(s, nil, ev(kernel.EvCommit, "lane/p"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Read(1)
	if len(got) != 2 || got[0].Seq != 1 || got[0].Kind != "edit" || got[1].Seq != 2 || got[1].Kind != "commit" {
		t.Fatalf("log = %+v; want parked edit Seq 1 then commit Seq 2", got)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "events.pending", "*.json")); len(files) != 0 {
		t.Fatalf("%d pending files left", len(files))
	}
}
