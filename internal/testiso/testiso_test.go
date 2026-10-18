package testiso

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fileDigest is the sha256 of the file at path, or "absent" when there is none.
func fileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "absent"
	}
	if err != nil {
		t.Fatal(err)
	}
	return hexSum(data)
}

func hexSum(data []byte) string {
	sum := sha256.Sum256(data)
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 64)
	for _, b := range sum {
		out = append(out, digits[b>>4], digits[b&15])
	}
	return string(out)
}

// A fixture that runs a bare `git config --global` must write the sandbox's
// config, never the operator's (#1043). The digest of the real ~/.gitconfig is
// taken from outside the sandbox, at the home the process had before Main
// moved it.
func TestTestiso_GlobalConfigWriteStaysInTheSandbox(t *testing.T) {
	if realHome == "" {
		t.Fatal("Main did not record the home the process started with")
	}
	gitconfig := filepath.Join(realHome, ".gitconfig")
	if inside(filepath.Dir(os.Getenv("HOME")), realHome) {
		t.Fatalf("the real home %q is inside the sandbox", realHome)
	}
	before := fileDigest(t, gitconfig)

	out, err := exec.Command("git", "config", "--global", "user.name", "sandbox").CombinedOutput()
	if err != nil {
		t.Fatalf("git config --global: %v\n%s", err, out)
	}

	if after := fileDigest(t, gitconfig); after != before {
		t.Errorf("the real %s changed: sha256 %s -> %s", gitconfig, before, after)
	}
	got, err := exec.Command("git", "config", "--global", "--get", "user.name").Output() // stderr-ok: the value is asserted below
	if err != nil || strings.TrimSpace(string(got)) != "sandbox" {
		t.Errorf("the sandbox config lacks the write: %q, %v", got, err)
	}
}

// The checkout the tests run from is a real repository; Verify refuses it.
func TestTestiso_RefusesToRunInsideTheRealRepo(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(cwd, "..", "..") // internal/testiso -> the module root
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("setup: %q is not the module root: %v", root, err)
	}

	err = Verify(root)

	if err == nil || !strings.Contains(err.Error(), "real repository") {
		t.Fatalf("Verify(%q) = %v, want an error naming the real repository", root, err)
	}
}

func TestVerify_AcceptsADirectoryUnderTheSandbox(t *testing.T) {
	if err := Verify(t.TempDir()); err != nil {
		t.Errorf("Verify(t.TempDir()) = %v, want nil", err)
	}
}

func TestVerify_RefusesADirectoryOutsideTheSandboxThatHoldsNoRepo(t *testing.T) {
	err := Verify(realHome)
	if err == nil || !strings.Contains(err.Error(), "outside the sandbox") {
		t.Errorf("Verify(%q) = %v, want an error naming the sandbox", realHome, err)
	}
}

// Repo hands each test a repository of its own, on main, with one commit, and
// inside the sandbox.
func TestRepo_HandsOutFreshCommittedReposUnderTheSandbox(t *testing.T) {
	one, two := Repo(t), Repo(t)

	if one == two {
		t.Fatalf("two calls returned the same repo %q", one)
	}
	for _, repo := range []string{one, two} {
		if err := Verify(repo); err != nil {
			t.Errorf("Verify(%q) = %v", repo, err)
		}
		if got := templateGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
			t.Errorf("%s: branch = %q, want main", repo, got)
		}
		if got := templateGit(t, repo, "log", "--format=%s"); got != "init" {
			t.Errorf("%s: log = %q, want the one init commit", repo, got)
		}
		if got := templateGit(t, repo, "status", "--porcelain"); got != "" {
			t.Errorf("%s: a fresh repo is dirty:\n%s", repo, got)
		}
	}
	if err := os.WriteFile(filepath.Join(one, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(two, "x.txt")); err == nil {
		t.Error("a file written in one repo appeared in another")
	}
}
