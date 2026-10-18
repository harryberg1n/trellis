package walls_test

import (
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/testiso"
)

// withUpstream gives the primary's default branch origin/main as its upstream,
// in the config only: the wall reads no ref.
func withUpstream(t testing.TB, w world) {
	t.Helper()
	testiso.Git(t, w.primary, "config", "branch.main.remote", "origin")
	testiso.Git(t, w.primary, "config", "branch.main.merge", "refs/heads/main")
}

// Live deny on honest work, fanpyp box, 2026-10-18 (v0.0.9): `git merge -q
// --ff-only origin/main` in the primary was refused as a main commit. Moving
// main to its own upstream lands nothing unjudged: those commits were judged on
// the way into origin/main.
func TestMainCommit_MergingTheOwnUpstreamIntoThePrimaryPasses(t *testing.T) {
	w := newWorld(t)
	withUpstream(t, w)
	r := quiet(t)
	p := filepath.ToSlash(w.primary) // a backslash path is an escape to bash
	for _, cmd := range []string{
		"cd " + p + " && git fetch -q && git merge -q --ff-only origin/main",
		"git -C " + p + " merge origin/main",
		"git -C " + p + " merge @{u}",
		"git -C " + p + " merge --ff-only @{upstream}",
		"git -C " + p + " merge",
	} {
		if d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny); len(d) != 0 {
			t.Errorf("%q: denies %+v, want none", cmd, d)
		}
	}
}

func TestMainCommit_MergingAnythingElseIntoThePrimaryIsStillDenied(t *testing.T) {
	w := newWorld(t)
	withUpstream(t, w)
	r := quiet(t)
	p := filepath.ToSlash(w.primary) // a backslash path is an escape to bash
	for _, cmd := range []string{
		"git -C " + p + " merge --ff-only unmerged",
		"git -C " + p + " merge origin/main unmerged",
		"git -C " + p + " merge origin/feature",
		"git -C " + p + " merge --no-ff origin/main",
	} {
		d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny)
		if len(d) != 1 || d[0].RowID != "main-commit" {
			t.Errorf("%q: denies %+v, want main-commit", cmd, d)
		}
	}
}

func TestMainCommit_WithoutAnUpstreamTheWallStands(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	p := filepath.ToSlash(w.primary) // a backslash path is an escape to bash
	for _, cmd := range []string{
		"git -C " + p + " merge --ff-only origin/main",
		"git -C " + p + " merge",
	} {
		d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny)
		if len(d) != 1 || d[0].RowID != "main-commit" {
			t.Errorf("%q with no upstream: denies %+v, want main-commit", cmd, d)
		}
	}
}
