package measure

import (
	"slices"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// HookLatency is the time one hook took, over the events it wrote that carry a
// time. P50 and P95 are in milliseconds, nearest-rank. Criterion is the p95
// limit in milliseconds, 0 for a hook with none; Holds is P95 <= Criterion.
type HookLatency struct {
	Hook      string
	N         int
	P50, P95  int64
	Criterion int64
	Holds     bool
}

// The p95 limits, on the owner's box: the edit path (PreToolUse and
// PostToolUse) and Stop.
const (
	editCriterionMS = 20
	stopCriterionMS = 300
)

// latencyHooks is the hooks in report order with the event kinds each one
// writes. A PreToolUse writes only when a wall or the policy acts, so its
// sample is the calls that were denied or overridden.
var latencyHooks = []struct {
	hook      string
	kinds     []string
	criterion int64
}{
	{kernel.HookPreToolUse, []string{kernel.EvWallDenied, kernel.EvWallOverridden, kernel.EvPolicyDenied}, editCriterionMS},
	{kernel.HookPostToolUse, []string{kernel.EvEdit}, editCriterionMS},
	{kernel.HookStop, []string{kernel.EvTurnAnchored}, stopCriterionMS},
	{kernel.HookSessionEnd, []string{kernel.EvSessionUsage}, 0},
}

// Latency groups the events that carry a hook time (TookMS > 0) by the hook
// that wrote them, in the order PreToolUse, PostToolUse, Stop, SessionEnd,
// and leaves out a hook with no such event.
func Latency(evs []kernel.Event) []HookLatency {
	var out []HookLatency
	for _, h := range latencyHooks {
		var ms []int64
		for _, e := range evs {
			if e.TookMS > 0 && slices.Contains(h.kinds, e.Kind) {
				ms = append(ms, e.TookMS)
			}
		}
		if len(ms) == 0 {
			continue
		}
		slices.Sort(ms)
		p95 := nearestRank(ms, 95)
		out = append(out, HookLatency{
			Hook: h.hook, N: len(ms),
			P50: nearestRank(ms, 50), P95: p95,
			Criterion: h.criterion,
			Holds:     h.criterion == 0 || p95 <= h.criterion,
		})
	}
	return out
}

// nearestRank is the value at rank ceil(p/100 * n) of sorted, which is not empty.
func nearestRank(sorted []int64, p int) int64 {
	rank := (p*len(sorted) + 99) / 100
	return sorted[rank-1]
}
