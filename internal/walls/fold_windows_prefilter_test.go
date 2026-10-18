//go:build windows

package walls_test

import (
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// The prefilters that decide whether to read a command at all fold case too.
func TestUndercover_CapitalisedGitAndGhReachTheTextRead(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	for _, cmd := range []string{
		`GIT commit -m "written by Claude"`,
		`GH pr create -t "written by Claude" --body ok`,
	} {
		d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny)
		if len(d) != 1 || d[0].RowID != "undercover" {
			t.Errorf("%q: denies %+v, want one from undercover", cmd, d)
		}
	}
}
