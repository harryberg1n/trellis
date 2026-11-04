package walls_test

import (
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/testiso"
)

// Live denies on honest work, fanpyp and threadspyp boxes, 2026-10-25 and
// 2026-11-04 (v0.0.13, issues #50 and #52): a push from the primary on main
// that writes only other refs on origin was refused as a main commit, and the
// way around it was `gh api -X DELETE`, which no wall sees. A push is judged by
// the refs it writes on the remote, not by the branch the checkout is on.
func TestMainCommit_APushThatWritesOnlyOtherRefsPasses(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	p := filepath.ToSlash(w.primary) // a backslash path is an escape to bash
	for _, args := range []string{
		"origin 1166a9e3be68:refs/heads/backup/wt/notice-strike-open",
		"origin HEAD:refs/heads/backup/wt/one HEAD~1:refs/heads/backup/wt/two",
		"--force-with-lease origin +HEAD:backup/x",
		"origin --delete notice-strike-open",
		"-d origin old-one old-two",
		"origin :old-branch",
		"origin :refs/heads/old-branch",
		"origin feature",
		"-u origin refs/heads/feature",
		"-o ci.skip origin feature",
		"--push-option ci.skip origin feature",
		"--receive-pack git-receive-pack origin feature",
		"origin tag v1.0",
		"origin refs/tags/v1.0",
		"origin main:refs/tags/main-2026-11-04",
		"--tags origin",
		"origin HEAD:refs/heads/main-backup 2>&1 | tail -1",
		"-n origin main",
	} {
		cmd := "git -C " + p + " push " + args
		if d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny); len(d) != 0 {
			t.Errorf("%q: denies %+v, want none", cmd, d)
		}
	}
}

func TestMainCommit_APushThatCanWriteTheDefaultBranchIsStillDenied(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	p := filepath.ToSlash(w.primary)
	for _, args := range []string{
		"",
		"origin",
		"-u origin",
		"--repo=origin",
		"--repo origin",
		"-o ci.skip origin",
		"origin main",
		"origin refs/heads/main",
		"origin HEAD",
		"origin @",
		"origin HEAD:main",
		"origin feature:refs/heads/main",
		"origin +1166a9e3be68:heads/main",
		"origin feature main",
		"origin --delete main",
		"-d origin old refs/heads/main",
		"origin :main",
		"origin :refs/heads/main",
		"--all origin",
		"--branches origin",
		"--mirror origin",
		"--prune origin feature",
		"origin 'refs/heads/*:refs/heads/*'",
		"--tags origin main",
		"origin main 2>&1 | tail -1",
	} {
		cmd := "git -C " + p + " push " + args
		d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny)
		if len(d) != 1 || d[0].RowID != "main-commit" {
			t.Errorf("%q: denies %+v, want main-commit", cmd, d)
		}
	}
}

// A remote with push refspecs in its config maps a bare `<src>` to a
// destination the command line does not show, so the wall cannot tell.
func TestMainCommit_APushThroughConfiguredPushRefspecsIsDenied(t *testing.T) {
	w := newWorld(t)
	testiso.Git(t, w.primary, "config", "remote.origin.push", "refs/heads/feature:refs/heads/main")
	r := quiet(t)
	cmd := "git -C " + filepath.ToSlash(w.primary) + " push origin feature"
	d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny)
	if len(d) != 1 || d[0].RowID != "main-commit" {
		t.Errorf("%q: denies %+v, want main-commit", cmd, d)
	}
	// An explicit destination is not mapped.
	cmd = "git -C " + filepath.ToSlash(w.primary) + " push origin feature:refs/heads/backup/feature"
	if d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny); len(d) != 0 {
		t.Errorf("%q: denies %+v, want none", cmd, d)
	}
}
