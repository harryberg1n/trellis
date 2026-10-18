package engine

import (
	"errors"
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

func ev(kind, tree string) kernel.Event {
	return kernel.Event{Kind: kind, Tree: tree, Session: "s1", At: t0}
}

func TestEngine_StampsArmsFromStickyBeforeAppend(t *testing.T) {
	s, _, _ := open(t)
	s.Shares = map[string]int{"exp1": 100, "exp2": 0}
	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/a"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	// A share change after the tree's first event must not move it.
	s.Shares = map[string]int{"exp1": 0, "exp2": 100}
	if _, err := Step(s, nil, ev(kernel.EvCommit, "tree/a"), kernel.Context{}); err != nil {
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
	first := ev(kernel.EvEdit, "tree/b")
	first.Arms = map[string]string{"exp1": "control"}
	if _, err := Step(s, nil, first, kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	s.Shares = map[string]int{"exp1": 100}
	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/b"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Read(1)
	if got[1].Arms["exp1"] != "control" {
		t.Fatalf("second event Arms = %v, want exp1=control", got[1].Arms)
	}
}

func TestEngine_FlagsEventsAfterMergeAndPersistsTreeState(t *testing.T) {
	s, _, _ := open(t)
	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/m"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Step(s, nil, ev(kernel.EvTreeMerged, "tree/m"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/m"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
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
	acts, err := Step(s, rows, ev(kernel.EvEdit, "tree/a"), kernel.Context{})
	if err != nil || len(acts) != 0 {
		t.Fatalf("Step = %v, %v; the step 2 matcher matches nothing", acts, err)
	}
}

func TestEngine_CheckpointCarriesLastSeq(t *testing.T) {
	s, dir, _ := open(t)
	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/a"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/a"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"last_seq":2,"arms":{"tree/a":{}},"trees":{"tree/a":1}}`
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
		_, err := Step(s, nil, ev(kernel.EvEdit, "tree/p"), kernel.Context{})
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
	if _, err := Step(s, nil, ev(kernel.EvCommit, "tree/p"), kernel.Context{}); err != nil {
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

func TestEngine_ReplaysLogIntoCheckpointAfterFailedSave(t *testing.T) {
	s, dir, _ := open(t)
	first := ev(kernel.EvEdit, "tree/x")
	first.Arms = map[string]string{"exp1": "control"}

	s.SetRename(func(string, string) error { return errors.New("injected rename failure") })
	if _, err := Step(s, nil, first, kernel.Context{}); err == nil {
		t.Fatal("Step succeeded despite the failed save")
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err == nil {
		t.Fatal("state.json exists after the failed save")
	}
	s.SetRename(os.Rename)

	// Without a replay the tree would be assigned treatment here.
	s.Shares = map[string]int{"exp1": 100}
	if _, err := Step(s, nil, ev(kernel.EvCommit, "tree/x"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"last_seq":2,"arms":{"tree/x":{"exp1":"control"}},"trees":{"tree/x":1}}`
	if string(raw) != want {
		t.Fatalf("state.json = %s, want %s", raw, want)
	}
	got, _ := s.Read(1)
	if len(got) != 2 || got[1].Arms["exp1"] != "control" {
		t.Fatalf("log = %+v; second event must carry exp1=control", got)
	}
}

func TestEngine_SteadyStateStepReadsTheLogTailOnce(t *testing.T) {
	s, dir, _ := open(t)
	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/a"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	before := s.BytesRead()
	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/a"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	// One read for the next Seq; none to catch the checkpoint up.
	if got := s.BytesRead() - before; got != fi.Size() {
		t.Fatalf("Step read %d log bytes, want %d (one tail read)", got, fi.Size())
	}
}

func TestFold_UndeletablePendingFileDoesNotWedge(t *testing.T) {
	s, dir, _ := open(t)
	if err := s.Park(ev(kernel.EvEdit, "tree/p")); err != nil {
		t.Fatal(err)
	}
	s.SetRemove(func(string) error { return errors.New("file is held open") })
	// The first Step folds the parked event; its file cannot be deleted.
	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/p"), kernel.Context{}); err != nil {
		t.Fatalf("first Step with an undeletable pending file: %v", err)
	}
	// The leftover is already logged: the next Step must succeed and not re-append it.
	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/p"), kernel.Context{}); err != nil {
		t.Fatalf("second Step with an undeletable pending file: %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "events.pending", "*.json"))
	if len(files) != 1 {
		t.Fatalf("%d pending files left, want the 1 undeletable one", len(files))
	}
	got, err := s.Read(1)
	if err != nil {
		t.Fatal(err)
	}
	parked := 0
	for _, e := range got {
		if e.Detail["park_id"] != "" {
			parked++
		}
	}
	if parked != 1 || len(got) != 3 {
		t.Fatalf("log has %d events, %d of them the parked one; want 3 and 1", len(got), parked)
	}
}

// A lazy log write lost to a power cut leaves state.json ahead of the log: its
// last_seq names events the log no longer holds.
func TestEngine_CheckpointAheadOfTheLogIsRebuiltFromTheLog(t *testing.T) {
	s, dir, _ := open(t)
	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/a"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}
	ahead := `{"last_seq":5,"arms":{"tree/ghost":{"exp1":"treatment"}},"trees":{"tree/ghost":1}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(ahead), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/a"), kernel.Context{}); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Read(1)
	if len(got) != 2 || got[1].Seq != 2 {
		t.Errorf("log = %+v, want the new event at Seq 2", got)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"last_seq":2,"arms":{"tree/a":{}},"trees":{"tree/a":1}}`
	if string(raw) != want {
		t.Errorf("state.json = %s, want %s", raw, want)
	}
}

func TestEngine_StampRunsInsideTheLockBeforeTheAppend(t *testing.T) {
	s, _, _ := open(t)
	stamp := WithStamp(func(e kernel.Event) kernel.Event {
		e.TookMS = 42
		return e
	})
	if _, err := Step(s, nil, ev(kernel.EvEdit, "tree/a"), kernel.Context{}, stamp); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(1)
	if err != nil || len(got) != 1 || got[0].TookMS != 42 {
		t.Fatalf("Read = %+v, %v; want one event with TookMS 42", got, err)
	}
}
