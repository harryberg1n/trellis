package update

import (
	"slices"
	"strconv"
	"strings"
)

// parseTag reads a release tag, v<MAJOR>.<MINOR>.<PATCH> and nothing else.
func parseTag(tag string) ([3]int, bool) {
	var v [3]int
	rest, ok := strings.CutPrefix(tag, "v")
	if !ok {
		return v, false
	}
	parts := strings.Split(rest, ".")
	if len(parts) != len(v) {
		return v, false
	}
	for i, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return v, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func compareTags(a, b string) int {
	va, _ := parseTag(a)
	vb, _ := parseTag(b)
	return slices.Compare(va[:], vb[:])
}

// keepNewest is how many pinned versions a sweep leaves on disk.
const keepNewest = 3

// sweepPlan is the pinned versions to remove, oldest first: every release tag
// but the newest keepNewest, unless keep names it, and only those update
// created. A directory that is not a
// release tag is never planned for removal.
func sweepPlan(pins []string, keep, created map[string]bool) []string {
	var tags []string
	for _, p := range pins {
		if _, ok := parseTag(p); ok {
			tags = append(tags, p)
		}
	}
	slices.SortFunc(tags, func(a, b string) int { return compareTags(b, a) })
	tags = slices.Compact(tags)
	var out []string
	for i := len(tags) - 1; i >= 0; i-- {
		if i >= keepNewest && !keep[tags[i]] && created[tags[i]] {
			out = append(out, tags[i])
		}
	}
	return out
}
