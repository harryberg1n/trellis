//go:build windows

package testiso

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This measures git for Windows 2.53: a git upgrade can move the limit without
// a trellis regression.
//
// Git for Windows refuses a worktree at a path of 216 characters with
// "fatal: '$GIT_DIR' too big" and takes 215, whatever core.longpaths says
// (measured with 2.53.0.windows.2). gitPathLimit must stay under the last
// accepted length, with room, or AddTree would build paths git then refuses.
func TestGitPathLimit_StaysUnderWhereGitForWindowsRefuses(t *testing.T) {
	primary := Repo(t)
	pathOf := func(n int) string {
		base := filepath.Join(canonical(t.TempDir()), "w")
		return base + strings.Repeat("x", n-len(base))
	}
	accepted, refused := pathOf(215), pathOf(216)

	Git(t, primary, "worktree", "add", "-q", "-b", "at-215", accepted)
	// exec-ok: the refusal is the thing measured; Git would fail the test on it.
	out, err := exec.Command("git", "-C", primary, "worktree", "add", "-q", "-b", "at-216", refused).CombinedOutput()

	if len(accepted) != 215 || len(refused) != 216 {
		t.Fatalf("path lengths = %d, %d, want 215, 216", len(accepted), len(refused))
	}
	if err == nil || !strings.Contains(string(out), "'$GIT_DIR' too big") {
		t.Errorf("git took a worktree path of 216 characters (err %v): %s", err, out)
	}
	if limit := gitPathLimit(); limit != 210 {
		t.Errorf("gitPathLimit() = %d, want 210: five characters under the 215 git takes", limit)
	}
}
