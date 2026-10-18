package measure

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Report is the plain-text report: aggregates only. It names no path, diff,
// prompt, transcript text, session or tree; table row ids are the only names.
// It names the model ids it could not price; Published does not.
func Report(f Facts) []byte { return render(f, false) }

// Published is the report as it may be filed on a public repository: a model
// id is replaced by its size label.
func Published(f Facts) []byte { return render(f, true) }

func render(f Facts, mask bool) []byte {
	var b bytes.Buffer
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	labels := Labels(f)
	counts := map[Label]int{}
	for _, l := range labels {
		counts[l]++
	}
	p("trellis report")
	for _, what := range f.NotRead {
		p("not read: %s", what)
	}
	p("trees merged: %d (defect %d, clean %d, pending %d)", len(labels), counts[Defect], counts[Clean], counts[Pending])

	p("")
	p("speed: tree opened to merged, minutes, per ISO week of the merge")
	speed := Speed(f)
	if len(speed) == 0 {
		p("  none")
	}
	for _, w := range speed {
		p("  %s  n=%d  p50=%.1f  p90=%.1f", w.Week, w.N, w.P50.Minutes(), w.P90.Minutes())
	}
	leads := LeadTimes(f)
	mins := make([]float64, 0, len(leads))
	for _, l := range leads {
		mins = append(mins, l.Time.Minutes())
	}
	sort.Float64s(mins) // the order of the log must not move the interval
	if len(mins) < MinIntervalSamples {
		p("  mean %.1f minutes, n=%d, no interval", mean(mins), len(mins))
	} else {
		lo, hi := Bootstrap(mins)
		p("  mean %.1f minutes, 90%% interval [%.1f, %.1f] (bootstrap, seed %d, %d resamples)", mean(mins), lo, hi, Seed, Resamples)
	}

	p("")
	p("cost: agent dollars at list price")
	costs, unpriced := Costs(f)
	trees := sortedKeys(costs)
	dollars := make([]float64, 0, len(trees))
	var total float64
	for _, tree := range trees {
		dollars = append(dollars, costs[tree])
		total += costs[tree]
	}
	p("  trees with usage: %d  total $%.5f", len(dollars), total)
	if unpriced.Tokens > 0 {
		p("  unpriced tokens: %d (models: %s)", unpriced.Tokens, strings.Join(modelNames(unpriced.Models, mask), ", "))
	}
	if f.MissingTranscripts > 0 {
		p("  transcripts missing: %d", f.MissingTranscripts)
	}
	if n := f.SkippedFiles; n > 0 {
		p("  transcript files not read: %d", n)
	}
	if n := SkippedLines(f); n > 0 {
		p("  transcript lines skipped: %d", n)
	}
	if len(dollars) < MinIntervalSamples {
		p("  per tree: mean $%.5f, n=%d, no interval", mean(dollars), len(dollars))
	} else {
		lo, hi := Bootstrap(dollars)
		p("  per tree: mean $%.5f, 90%% interval [$%.5f, $%.5f]", mean(dollars), lo, hi)
	}
	p("  net_cost inputs per row (injected tokens = bytes / 4; turns from a deny to the next green)")
	rows := RowCosts(f)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Row < rows[j].Row })
	if len(rows) == 0 {
		p("  none")
	}
	for _, r := range rows {
		p("  %s  denies=%d  injected_tokens=%d  turns=%d  unresolved=%d  unresolved_turns=%d", r.Row, r.Denies, r.InjectedTokens, r.Turns, r.Unresolved, r.UnresolvedTurns)
	}
	p("")
	latencyBlock(p, Latency(f.Events))
	p("")
	p("as of %s", f.Now.UTC().Format(time.RFC3339))
	return b.Bytes()
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// latencyBlock prints the hook latency lines. The times are the hooks' own,
// from the start of the hook command to just before its event was appended,
// so lock wait and the turn anchor are inside and process start is not.
func latencyBlock(p func(string, ...any), lat []HookLatency) {
	if len(lat) == 0 {
		p("hook latency: none")
		return
	}
	var n int
	for _, l := range lat {
		n += l.N
	}
	p("hook latency: p50 / p95 ms over %d timed events (lock wait and anchor included; append, checkpoint, fsync and process start excluded)", n)
	for _, l := range lat {
		verdict := "(no criterion)"
		if l.Criterion > 0 {
			ok := "ok"
			if !l.Holds {
				ok = "over"
			}
			verdict = fmt.Sprintf("(criterion %d: %s)", l.Criterion, ok)
		}
		p("  %-24s p50 %-4d p95 %-4d n %-5d %s", l.Hook, l.P50, l.P95, l.N, verdict)
	}
}

// modelNames are the unpriced ids as shown: as they are, or, for a report that
// is filed, as the sorted, distinct size labels.
func modelNames(ids []string, mask bool) []string {
	if !mask {
		return ids
	}
	set := map[string]bool{}
	for _, id := range ids {
		set[SizeLabel(id)] = true
	}
	return sortedKeys(set)
}
