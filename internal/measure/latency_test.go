package measure

import (
	"reflect"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
)

func took(kind string, ms int64) kernel.Event { return kernel.Event{Kind: kind, TookMS: ms} }

func tooks(kind string, ms ...int64) []kernel.Event {
	out := make([]kernel.Event, len(ms))
	for i, m := range ms {
		out[i] = took(kind, m)
	}
	return out
}

func TestLatency_NearestRankPerKind(t *testing.T) {
	var evs []kernel.Event
	// PostToolUse: 1..20 ms. Nearest rank: p50 = 10th = 10, p95 = 19th = 19.
	for i := int64(1); i <= 20; i++ {
		evs = append(evs, took(kernel.EvEdit, i))
	}
	// Stop: 5 samples. p50 = ceil(2.5) = 3rd = 30, p95 = ceil(4.75) = 5th = 400.
	evs = append(evs, tooks(kernel.EvTurnAnchored, 400, 10, 40, 20, 30)...)
	// SessionEnd: one sample.
	evs = append(evs, took(kernel.EvSessionUsage, 7))
	// PreToolUse is seen through the wall events it writes.
	evs = append(evs, tooks(kernel.EvWallDenied, 3)...)
	evs = append(evs, tooks(kernel.EvPolicyDenied, 9)...)
	evs = append(evs, tooks(kernel.EvWallOverridden, 5)...)

	got := Latency(evs)
	want := []HookLatency{
		{Hook: "PreToolUse", N: 3, P50: 5, P95: 9, Criterion: 20, Holds: true},
		{Hook: "PostToolUse", N: 20, P50: 10, P95: 19, Criterion: 20, Holds: true},
		{Hook: "Stop", N: 5, P50: 30, P95: 400, Criterion: 300, Holds: false},
		{Hook: "SessionEnd", N: 1, P50: 7, P95: 7, Criterion: 0, Holds: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Latency = %+v\nwant     %+v", got, want)
	}
}

func TestLatency_CriterionIsInclusiveAtTheLimit(t *testing.T) {
	got := Latency(append(tooks(kernel.EvEdit, 20), tooks(kernel.EvTurnAnchored, 301)...))
	if len(got) != 2 || !got[0].Holds || got[1].Holds {
		t.Fatalf("Latency = %+v, want PostToolUse at 20 holding and Stop at 301 not", got)
	}
}

func TestLatency_IgnoresUnmeasuredAndOtherKinds(t *testing.T) {
	evs := []kernel.Event{
		took(kernel.EvEdit, 0), // a legacy record
		took(kernel.EvCommit, 50),
		took(kernel.EvInternalError, 50),
		took(kernel.EvTreeOpened, 50),
	}
	if got := Latency(evs); len(got) != 0 {
		t.Fatalf("Latency = %+v, want none", got)
	}
}

func TestLatency_DoesNotReorderItsInput(t *testing.T) {
	evs := tooks(kernel.EvEdit, 9, 1, 5)
	Latency(evs)
	if evs[0].TookMS != 9 || evs[1].TookMS != 1 || evs[2].TookMS != 5 {
		t.Fatalf("input reordered: %+v", evs)
	}
}
