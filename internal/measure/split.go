package measure

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// SplitPart is one part of the Stop hook's anchor over the turns that carry a
// split: the whole anchor, one kind of git child, or the rest (the hook's own
// hashing and reading of files). P50 and P95 are milliseconds, nearest-rank,
// and a turn in which a kind did not run counts as 0 for it, so every part is
// over the same N turns. Runs50 and Runs95 are how many children of the kind
// ran in a turn; they are 0 for the anchor and the rest.
type SplitPart struct {
	Name           string
	N              int
	P50, P95       int64
	Runs50, Runs95 int64
}

// The names of the two parts that are not a git child.
const (
	splitAnchor = "anchor"
	splitRest   = "rest"
)

// gitPart is what one turn's kernel.DetailGitMS says of one kind.
type gitPart struct{ ms, runs int64 }

// StopSplit is the parts of the anchor over the turn.anchored events that
// carry a readable split: the anchor first, the git kinds by falling p95 (then
// by name), the rest last. It is nil when no event carries one. An event whose
// split cannot be read is left out whole.
func StopSplit(evs []kernel.Event) []SplitPart {
	var anchors, rests []int64
	var turns []map[string]gitPart
	kinds := map[string]bool{}
	for _, e := range evs {
		if e.Kind != kernel.EvTurnAnchored {
			continue
		}
		anchor, err := strconv.ParseInt(e.Detail[kernel.DetailAnchorMS], 10, 64)
		if err != nil || anchor < 0 {
			continue
		}
		parts, ok := parseGitMS(e.Detail[kernel.DetailGitMS])
		if !ok {
			continue
		}
		rest := anchor
		for k, p := range parts {
			kinds[k] = true
			rest -= p.ms
		}
		anchors = append(anchors, anchor)
		rests = append(rests, max(rest, 0))
		turns = append(turns, parts)
	}
	if len(anchors) == 0 {
		return nil
	}
	n := len(anchors)
	out := []SplitPart{{Name: splitAnchor, N: n, P50: percentile(anchors, 50), P95: percentile(anchors, 95)}}
	var git []SplitPart
	for k := range kinds {
		ms, runs := make([]int64, 0, n), make([]int64, 0, n)
		for _, t := range turns {
			ms, runs = append(ms, t[k].ms), append(runs, t[k].runs)
		}
		git = append(git, SplitPart{Name: k, N: n, P50: percentile(ms, 50), P95: percentile(ms, 95), Runs50: percentile(runs, 50), Runs95: percentile(runs, 95)})
	}
	slices.SortFunc(git, func(a, b SplitPart) int {
		return cmp.Or(cmp.Compare(b.P95, a.P95), cmp.Compare(a.Name, b.Name))
	})
	out = append(out, git...)
	return append(out, SplitPart{Name: splitRest, N: n, P50: percentile(rests, 50), P95: percentile(rests, 95)})
}

// percentile sorts xs, which is not empty, and returns its nearest-rank percentile p.
func percentile(xs []int64, p int) int64 {
	slices.Sort(xs)
	return nearestRank(xs, p)
}

// parseGitMS reads "kind=ms/runs" fields joined by spaces. An empty value is
// an anchor that ran no child.
func parseGitMS(s string) (map[string]gitPart, bool) {
	parts := map[string]gitPart{}
	for _, f := range strings.Fields(s) {
		kind, rest, ok := strings.Cut(f, "=")
		msText, runsText, ok2 := strings.Cut(rest, "/")
		if !ok || !ok2 || kind == "" {
			return nil, false
		}
		ms, err := strconv.ParseInt(msText, 10, 64)
		if err != nil || ms < 0 {
			return nil, false
		}
		runs, err := strconv.ParseInt(runsText, 10, 64)
		if err != nil || runs < 0 {
			return nil, false
		}
		parts[kind] = gitPart{ms: ms, runs: runs}
	}
	return parts, true
}
