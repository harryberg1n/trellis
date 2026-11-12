package hook

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
)

// costly moves the fake clock by what a git child of that kind costs, then
// runs it for real: the clock is the only source of the times a test reads.
type costly struct {
	inner run.Runner
	clk   *clock.FakeClock
	cost  map[string]time.Duration
}

var _ run.Runner = costly{}

func (c costly) Light(spec run.Spec) (run.Result, error) {
	c.clk.Advance(c.cost[run.Kind(spec)])
	return c.inner.Light(spec)
}

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// Incident: the M0 exit check of 2026-11-12 found Stop at p95 709 and 720 ms
// with one number for the whole anchor, so nothing said which part to fix.
func TestSplit_ACleanStopNamesEachGitChildOfTheAnchor(t *testing.T) {
	e := newEnv(t)
	clk := clock.Fake(timingStart.Add(ms(10)))
	e.deps.Start, e.deps.Clock = timingStart, clk
	e.deps.Runner = costly{inner: e.rec, clk: clk, cost: map[string]time.Duration{
		"status": ms(120), "rev-parse": ms(3), "commit-tree": ms(6), "update-ref": ms(7),
	}}

	e.do(t, "Stop", fill(readShape(t, "stop"), vars(e.repo, "", "")))

	evs := e.events(t)
	if len(evs) != 1 || evs[0].Kind != "turn.anchored" {
		t.Fatalf("events %+v, want one turn.anchored", evs)
	}
	if got, want := evs[0].Detail["anchor_ms"], "136"; got != want {
		t.Errorf("anchor_ms = %q, want %q", got, want)
	}
	if got, want := evs[0].Detail["git_ms"], "commit-tree=6/1 rev-parse=3/1 status=120/1 update-ref=7/1"; got != want {
		t.Errorf("git_ms = %q, want %q", got, want)
	}
	if evs[0].TookMS != 146 {
		t.Errorf("TookMS = %d, want 146", evs[0].TookMS)
	}
}

func TestSplit_AStopWithChangesCountsOneMktreePerDirectoryLevel(t *testing.T) {
	e := newEnv(t)
	testiso.Write(t, filepath.Join(e.repo, "a", "b", "x.txt"), "x\n")
	clk := clock.Fake(timingStart)
	e.deps.Start, e.deps.Clock = timingStart, clk
	e.deps.Runner = costly{inner: e.rec, clk: clk, cost: map[string]time.Duration{
		"status": ms(100), "cat-file": ms(8), "mktree": ms(10), "commit-tree": ms(6), "update-ref": ms(7),
	}}

	e.do(t, "Stop", fill(readShape(t, "stop"), vars(e.repo, "", "")))

	evs := e.events(t)
	if len(evs) != 1 {
		t.Fatalf("events %+v, want one", evs)
	}
	if got, want := evs[0].Detail["anchor_ms"], "151"; got != want {
		t.Errorf("anchor_ms = %q, want %q", got, want)
	}
	if got, want := evs[0].Detail["git_ms"], "cat-file=8/1 commit-tree=6/1 mktree=30/3 status=100/1 update-ref=7/1"; got != want {
		t.Errorf("git_ms = %q, want %q", got, want)
	}
}

func TestSplit_AnUnmeasuredStopCarriesNoSplit(t *testing.T) {
	e := newEnv(t)
	e.do(t, "Stop", fill(readShape(t, "stop"), vars(e.repo, "", "")))
	evs := e.events(t)
	if len(evs) != 1 {
		t.Fatalf("events %+v, want one", evs)
	}
	for _, k := range []string{"anchor_ms", "git_ms"} {
		if v, ok := evs[0].Detail[k]; ok {
			t.Errorf("detail %s = %q without a start, want it absent", k, v)
		}
	}
}
