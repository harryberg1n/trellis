//go:build !windows

package walls_test

import (
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// A file system that tells Git from git does not run the command a capitalised
// name spells as git, so the walls leave it alone.
func TestWalls_CommandNamesStayCaseSensitiveOffWindows(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	for _, cmd := range []string{`Git reset --hard`, `GIT commit --no-verify -m x`} {
		if denies := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny); len(denies) != 0 {
			t.Errorf("%q: denies %+v, want none", cmd, denies)
		}
	}
}
