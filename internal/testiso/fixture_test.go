package testiso

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCountGitSpawns_CountsEachGitStart(t *testing.T) {
	repo := Repo(t)
	count := CountGitSpawns(t)
	if n := count(); n != 0 {
		t.Fatalf("count before any spawn = %d", n)
	}
	Git(t, repo, "status")
	Git(t, repo, "rev-parse", "HEAD")
	if n := count(); n != 2 {
		t.Errorf("count after two git runs = %d, want 2", n)
	}
}

func TestFakeTool_ExitsWithItsCodeAndSpeaksOnStderr(t *testing.T) {
	FakeTool(t, "trellis-fake", 3, "leaks found: 2")
	var errb strings.Builder
	cmd := exec.Command("trellis-fake")
	cmd.Stderr = &errb
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || !strings.Contains(errb.String(), "leaks found: 2") {
		t.Errorf("err %v, stderr %q; want exit 3 and the stderr text", err, errb.String())
	}
}

func TestPathWithOnly_FindsTheNamedToolsAndNothingElse(t *testing.T) {
	FakeTool(t, "trellis-fake", 0, "")
	PathWithOnly(t, "git")
	if _, err := exec.LookPath("git"); err != nil {
		t.Errorf("git not found: %v", err)
	}
	if _, err := exec.LookPath("trellis-fake"); err == nil {
		t.Error("a tool outside the list is still found")
	}
	PathWithOnly(t)
	if _, err := exec.LookPath("git"); err == nil {
		t.Error("git is found on a PATH with no tools")
	}
}

func TestGitHelpers_CommitAsTheTemplateIdentity(t *testing.T) {
	repo := Repo(t)
	Write(t, repo+"/new/f.txt", "x\n")
	Git(t, repo, "add", "-A")
	CommitAll(t, repo, "second")
	if got := Git(t, repo, "log", "-1", "--format=%an <%ae> %s"); got != AuthorName+" <"+AuthorEmail+"> second" {
		t.Errorf("last commit %q", got)
	}
}

func TestAddTree_LivesUnderAShortSandboxPathNotBesideThePrimary(t *testing.T) {
	primary := Repo(t)

	tree := AddTree(t, primary, "feat-tree", "feat")

	if want := filepath.Join(canonical(sandboxRoot), "wt") + string(filepath.Separator); !strings.HasPrefix(canonical(tree), want) {
		t.Errorf("tree = %q, want it under %q", tree, want)
	}
	if filepath.Base(tree) != "feat-tree" {
		t.Errorf("tree = %q, want the directory named feat-tree", tree)
	}
	if err := Verify(tree); err != nil {
		t.Errorf("Verify(tree) = %v, want the tree inside the sandbox", err)
	}
	if got := Git(t, tree, "symbolic-ref", "--short", "HEAD"); got != "feat" {
		t.Errorf("branch of the tree = %q, want feat", got)
	}
}

func TestAddTree_TreesOfOneNameDoNotCollide(t *testing.T) {
	a := AddTree(t, Repo(t), "tree", "x")
	b := AddTree(t, Repo(t), "tree", "x")

	if a == b {
		t.Errorf("two trees share the path %q", a)
	}
}

func TestAddTree_TheTreeIsGoneWhenTheTestEnds(t *testing.T) {
	var tree string
	t.Run("inner", func(t *testing.T) {
		tree = AddTree(t, Repo(t), "tree", "x")
	})

	if _, err := os.Stat(tree); !os.IsNotExist(err) {
		t.Errorf("stat %s = %v, want it removed", tree, err)
	}
}

func TestTreePathError_NamesTheLimitAndTheLength(t *testing.T) {
	long := strings.Repeat("d", 211)

	err := treePathError(long, 210)

	want := "testiso.AddTree: the worktree path is 211 characters; git for Windows refuses a worktree path over 210"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if err := treePathError(strings.Repeat("d", 210), 210); err != nil {
		t.Errorf("a path at the limit: %v", err)
	}
	if err := treePathError(long, 0); err != nil {
		t.Errorf("no limit, yet: %v", err)
	}
}
