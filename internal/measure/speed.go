package measure

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// WeekSpeed is the lead times of the lanes merged in one ISO week.
type WeekSpeed struct {
	Week     string
	N        int
	P50, P90 time.Duration
}

// LeadTimes is each merged lane's time from lane opened to merged, in the
// order of its merge. The open time is the lane's lane.opened event, else its
// first event; a lane with no event, or one merged before it opened, has no
// lead time.
func LeadTimes(f Facts) map[string]time.Duration {
	opened, first := map[string]time.Time{}, map[string]time.Time{}
	for _, ev := range f.Events {
		if ev.Lane == "" {
			continue
		}
		if at, ok := first[ev.Lane]; !ok || ev.At.Before(at) {
			first[ev.Lane] = ev.At
		}
		if at, ok := opened[ev.Lane]; ev.Kind == kernel.EvLaneOpened && (!ok || ev.At.Before(at)) {
			opened[ev.Lane] = ev.At
		}
	}
	out := map[string]time.Duration{}
	for _, m := range linked(f) {
		if m.Lane == "" {
			continue
		}
		at, ok := opened[m.Lane]
		if !ok {
			at, ok = first[m.Lane]
		}
		if !ok || m.At.Before(at) {
			continue
		}
		out[m.Lane] = m.At.Sub(at)
	}
	return out
}

// Speed is the p50 and p90 lead time per ISO week of the merge, oldest week
// first. A percentile is the nearest rank.
func Speed(f Facts) []WeekSpeed {
	lead := LeadTimes(f)
	byWeek := map[string][]time.Duration{}
	for _, m := range linked(f) {
		d, ok := lead[m.Lane]
		if m.Lane == "" || !ok {
			continue
		}
		byWeek[week(m.At)] = append(byWeek[week(m.At)], d)
	}
	var out []WeekSpeed
	for _, w := range sortedKeys(byWeek) {
		ds := byWeek[w]
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		out = append(out, WeekSpeed{Week: w, N: len(ds), P50: rank(ds, 0.5), P90: rank(ds, 0.9)})
	}
	return out
}

func rank(sorted []time.Duration, p float64) time.Duration {
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(i, 0)]
}

// Week is the ISO week of t in UTC, as "2026-W41".
func Week(t time.Time) string { return week(t) }

func week(t time.Time) string {
	y, w := t.UTC().ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}
