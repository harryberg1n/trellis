package hook

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/run"
)

// gitTimes is a Runner that adds up, per kind of child, how long the children
// it runs took and how many ran. It is used by one hook run, on one goroutine.
type gitTimes struct {
	inner run.Runner
	now   func() time.Time
	took  map[string]time.Duration
	runs  map[string]int
}

var _ run.Runner = (*gitTimes)(nil)

// Light runs spec on the inner runner and adds its time to its kind.
func (g *gitTimes) Light(spec run.Spec) (run.Result, error) {
	began := g.now()
	res, err := g.inner.Light(spec)
	if g.took == nil {
		g.took, g.runs = map[string]time.Duration{}, map[string]int{}
	}
	kind := run.Kind(spec)
	g.took[kind] += g.now().Sub(began)
	g.runs[kind]++
	return res, err
}

// String is the children as "kind=ms/runs" joined by spaces, kinds in name
// order: what kernel.DetailGitMS carries.
func (g *gitTimes) String() string {
	kinds := make([]string, 0, len(g.took))
	for k := range g.took {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, k+"="+strconv.FormatInt(kernel.Millis(g.took[k]), 10)+"/"+strconv.Itoa(g.runs[k]))
	}
	return strings.Join(parts, " ")
}
