//go:build windows

package walls_test

import (
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
)

func TestWalls_CommandNamesAreCaseFoldedOnWindows(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	for _, c := range []struct{ cmd, row string }{
		{`Git reset --hard`, "discard-uncommitted"},
		{`GIT commit --no-verify -m x`, "hook-bypass"},
		{`GIT.EXE push --no-verify`, "hook-bypass"},
	} {
		denies := verdicts(decide(t, bash(t, w.tree, c.cmd, true), r), kernel.VerdictDeny)
		if len(denies) != 1 || denies[0].RowID != c.row {
			t.Errorf("%q: denies %+v, want exactly one from %s", c.cmd, denies, c.row)
		}
	}
}
