package testiso

import (
	"errors"
	"os/exec"
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
