// Package measure folds recorded facts into the report: defect labels, lane
// speed, agent cost and intervals. It is pure: it imports the standard library
// and kernel types only, reads no file and no clock. Everything it knows comes
// in as Facts, gathered by cmd/trellis.
package measure

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// Window is how long after its merge a change can still become a defect.
const Window = 14 * 24 * time.Hour

// Label is a lane's verdict.
type Label string

// The labels. Pending holds until the window closes.
const (
	Defect  Label = "defect"
	Clean   Label = "clean"
	Pending Label = "pending"
)

// Span is an inclusive line range.
type Span struct{ Start, End int }

// Merge is one first-parent commit on the default branch. Added holds the
// new-side line ranges the commit wrote, Removed the old-side ranges it
// replaced or deleted, by path. Lane is the lane that produced it, empty for a
// commit no lane owns (a revert, a direct commit).
type Merge struct {
	Sha, Subject, Body, Lane string
	PR                       int
	At                       time.Time
	Added, Removed           map[string][]Span
}

// Issue is a GitHub issue: its number, title, body and creation time.
type Issue struct {
	Number      int
	Title, Body string
	At          time.Time
}

// Usage names one session transcript (raw JSONL bytes) and the lane it
// worked on.
type Usage struct {
	Lane, Session string
	Transcript    []byte
}

// PR is a merged pull request: its number, head branch and merge time.
type PR struct {
	Number   int
	Branch   string
	MergedAt time.Time
}

// Facts is everything the report reads. Now is the caller's clock.
type Facts struct {
	Merges []Merge
	Issues []Issue
	PRs    []PR
	Events []kernel.Event
	Usage  []Usage
	Now    time.Time
}

// linked is the merges with a lane filled in for each squash or rebase merge
// "(#N)" that has none: the lane whose name is the head branch of PR N. A lane
// already on the merge (a merge commit's branch, or a Detail["pr"] override
// resolved by the caller) is kept.
func linked(f Facts) []Merge {
	branch := map[int]string{}
	for _, p := range f.PRs {
		branch[p.Number] = p.Branch
	}
	out := make([]Merge, len(f.Merges))
	copy(out, f.Merges)
	for i, m := range out {
		if m.Lane == "" && m.PR > 0 {
			out[i].Lane = branch[m.PR]
		}
	}
	return out
}

var (
	prRef      = regexp.MustCompile(`#(\d+)`)
	revertsSha = regexp.MustCompile(`[Tt]his reverts commit ([0-9a-f]{7,64})`)
)

// laneKey is the key a merge's lane has in the maps: its lane name, else
// pr-N.
func laneKey(m Merge) string {
	if m.Lane != "" {
		return m.Lane
	}
	return fmt.Sprintf("pr-%d", m.PR)
}

// Labels labels every lane that has a merge with a PR number or a lane name.
// A change is a defect when, within Window of its merge, any of these happen:
// a revert of its commit; a later merge whose subject starts with "fix" and
// that touches its lines; a later commit or PR that names its PR; an "escape:"
// issue that names its PR. Without one of them it is Pending until the window
// closes at Now, then Clean. A lane with several merges takes the worst: defect, then
// pending, then clean. Line overlap alone is not a label: it is off
// until a precision check has validated it.
//
// "Touches its lines" compares the fix's old-side ranges with the change's
// new-side ranges by path, without following line shifts of merges between
// the two.
func Labels(f Facts) map[string]Label {
	rank := map[Label]int{Clean: 1, Pending: 2, Defect: 3} // a lane keeps its worst merge
	out := map[string]Label{}
	f.Merges = linked(f)
	for _, m := range f.Merges {
		if m.Lane == "" && m.PR == 0 {
			continue // a revert or direct commit owns no lane
		}
		l := Clean
		switch {
		case defective(f, m):
			l = Defect
		case f.Now.Before(m.At.Add(Window)):
			l = Pending
		}
		if k := laneKey(m); rank[l] > rank[out[k]] {
			out[k] = l
		}
	}
	return out
}

func defective(f Facts, m Merge) bool {
	end := m.At.Add(Window)
	for _, n := range f.Merges {
		if n.Sha == m.Sha || n.At.Before(m.At) || n.At.After(end) {
			continue
		}
		if reverts(n, m) || (fixPrefix(n.Subject) && overlaps(m.Added, n.Removed)) || (m.PR > 0 && n.PR != m.PR && names(n.Subject+"\n"+n.Body, m.PR)) {
			return true
		}
	}
	for _, is := range f.Issues {
		if is.At.Before(m.At) || is.At.After(end) {
			continue
		}
		if escapeTitle(is.Title) && m.PR > 0 && names(is.Title+"\n"+is.Body, m.PR) {
			return true
		}
	}
	return false
}

func reverts(n, m Merge) bool {
	if m.Sha == "" {
		return false
	}
	for _, g := range revertsSha.FindAllStringSubmatch(n.Body, -1) {
		if strings.HasPrefix(m.Sha, g[1]) {
			return true
		}
	}
	return false
}

func fixPrefix(subject string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "fix")
}

func escapeTitle(title string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(title)), "escape:")
}

// names reports whether text names PR n as #n, exactly.
func names(text string, n int) bool {
	for _, g := range prRef.FindAllStringSubmatch(text, -1) {
		if v, err := strconv.Atoi(g[1]); err == nil && v == n {
			return true
		}
	}
	return false
}

func overlaps(a, b map[string][]Span) bool {
	for p, as := range a {
		for _, x := range as {
			for _, y := range b[p] {
				if x.Start <= y.End && y.Start <= x.End {
					return true
				}
			}
		}
	}
	return false
}

// ParseHunks reads the output of `git show -U0 --format=` into the new-side
// ranges added and the old-side ranges removed, by path.
func ParseHunks(diff string) (added, removed map[string][]Span) {
	added, removed = map[string][]Span{}, map[string][]Span{}
	var oldPath, path string
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "--- "):
			oldPath = strings.TrimPrefix(strings.TrimPrefix(line, "--- "), "a/")
		case strings.HasPrefix(line, "+++ "):
			path = strings.TrimPrefix(line, "+++ ")
			if path == "/dev/null" {
				path = oldPath
			} else {
				path = strings.TrimPrefix(path, "b/")
			}
		case strings.HasPrefix(line, "@@ ") && path != "":
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			if s, ok := hunkSpan(fields[1], "-"); ok {
				removed[path] = append(removed[path], s)
			}
			if s, ok := hunkSpan(fields[2], "+"); ok {
				added[path] = append(added[path], s)
			}
		}
	}
	return added, removed
}

// hunkSpan reads "-3,2" or "+22,0" ("-20" means one line). A zero count has
// no lines.
func hunkSpan(s, sign string) (Span, bool) {
	start, count, hasCount := strings.Cut(strings.TrimPrefix(s, sign), ",")
	a, err := strconv.Atoi(start)
	if err != nil {
		return Span{}, false
	}
	n := 1
	if hasCount {
		if n, err = strconv.Atoi(count); err != nil {
			return Span{}, false
		}
	}
	if n == 0 {
		return Span{}, false
	}
	return Span{Start: a, End: a + n - 1}, true
}

// sortedKeys returns the map's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
