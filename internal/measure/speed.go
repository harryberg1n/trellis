package measure

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// WeekSpeed is the lead times of the trees merged in one ISO week.
type WeekSpeed struct {
	Week     string
	N        int
	P50, P90 time.Duration
}

// Lead is one merge's lead time: from its tree opening to the merge.
type Lead struct {
	Tree     string
	MergedAt time.Time
	Time     time.Duration
}

// LeadTimes is the lead time of each merge that has a tree, in the order of
// f.Merges. The open time is the tree's latest tree.opened event at or before
// the merge, so a tree opened again after an earlier merge measures from the
// reopening; with no such event it is the tree's first event at or before the
// merge. A merge with neither has no lead time.
func LeadTimes(f Facts) []Lead {
	var out []Lead
	for _, m := range linked(f) {
		if m.Tree == "" {
			continue
		}
		var opened, first time.Time
		foundOpen, foundFirst := false, false
		for _, ev := range f.Events {
			if ev.Tree != m.Tree || ev.At.After(m.At) {
				continue
			}
			if !foundFirst || ev.At.Before(first) {
				first, foundFirst = ev.At, true
			}
			if ev.Kind == kernel.EvTreeOpened && (!foundOpen || ev.At.After(opened)) {
				opened, foundOpen = ev.At, true
			}
		}
		switch {
		case foundOpen:
			out = append(out, Lead{Tree: m.Tree, MergedAt: m.At, Time: m.At.Sub(opened)})
		case foundFirst:
			out = append(out, Lead{Tree: m.Tree, MergedAt: m.At, Time: m.At.Sub(first)})
		}
	}
	return out
}

// Speed is the p50 and p90 lead time per ISO week of the merge, oldest week
// first. A percentile is the nearest rank.
func Speed(f Facts) []WeekSpeed {
	byWeek := map[string][]time.Duration{}
	for _, l := range LeadTimes(f) {
		byWeek[week(l.MergedAt)] = append(byWeek[week(l.MergedAt)], l.Time)
	}
	out := make([]WeekSpeed, 0, len(byWeek))
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
