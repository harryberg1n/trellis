// Package testsupport holds the test helpers that need run, clock or kernel: a
// spawn-counting runner, a fake runner and a reader of parked events. The rest
// (git, files, fake tools) is testiso's, which run's own tests can import.
// Production code does not import it.
package testsupport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/run"
)

// Recorder is a Runner over the real clock that counts what it spawns.
func Recorder() *run.Recorder {
	return &run.Recorder{Inner: run.Exec{Clock: clock.Real()}}
}

// FakeRunner answers the specs it is asked to run with Answer and records
// them. A spec whose program is not Name goes to Inner instead (every spec is
// answered when Name is empty), so a test fakes gh and still runs a real git.
type FakeRunner struct {
	Name   string
	Inner  run.Runner
	Answer func(run.Spec) (run.Result, error)

	mu    sync.Mutex
	specs []run.Spec
}

var _ run.Runner = (*FakeRunner)(nil)

// Light records spec and answers it, or hands it to Inner.
func (f *FakeRunner) Light(spec run.Spec) (run.Result, error) {
	if f.Name != "" && spec.Name != f.Name {
		return f.Inner.Light(spec)
	}
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	f.mu.Unlock()
	return f.Answer(spec)
}

// Specs is every spec the fake answered, in order.
func (f *FakeRunner) Specs() []run.Spec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]run.Spec(nil), f.specs...)
}

// Stdout is an Answer that gives out as the standard output of every spec.
func Stdout(out string) func(run.Spec) (run.Result, error) {
	return func(run.Spec) (run.Result, error) { return run.Result{Stdout: []byte(out)}, nil }
}

// PendingEvents reads the events parked in dir/events.pending, in file-name
// order. A directory with none gives none.
func PendingEvents(t testing.TB, dir string) []kernel.Event {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "events.pending", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]kernel.Event, 0, len(files))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var ev kernel.Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out = append(out, ev)
	}
	return out
}
