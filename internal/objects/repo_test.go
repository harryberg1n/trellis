package objects

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
	"github.com/harryberg1n/trellis/internal/testsupport"
)

func TestBranch(t *testing.T) {
	repo := testiso.Repo(t)
	git := filepath.Join(repo, ".git")
	if got, err := Branch(git); err != nil || got != "main" {
		t.Errorf("Branch = %q, %v; want main", got, err)
	}
	testsupport.Git(t, repo, "checkout", "-q", "-b", "feature/deep/x")
	if got, _ := Branch(git); got != "feature/deep/x" {
		t.Errorf("Branch = %q, want feature/deep/x", got)
	}
	testsupport.Git(t, repo, "checkout", "-q", "--detach")
	if got, _ := Branch(git); got != "detached" {
		t.Errorf("Branch = %q, want detached", got)
	}
}

func TestFindRoot(t *testing.T) {
	repo := testiso.Repo(t)
	got, err := FindRoot(filepath.Join(repo, "no", "such", "dir"))
	if err != nil || got != repo {
		t.Errorf("FindRoot = %q, %v; want %q", got, err, repo)
	}
	if _, err := FindRoot(filepath.Dir(repo)); !errors.Is(err, ErrNotARepo) {
		t.Errorf("FindRoot outside a repo: %v, want ErrNotARepo", err)
	}
}

func TestConfigValue_SubsectionKey(t *testing.T) {
	repo := testiso.Repo(t)
	testsupport.Git(t, repo, "remote", "add", "origin", "https://example.invalid/r.git")
	v, ok, err := ConfigValue(filepath.Join(repo, ".git"), `remote "origin"`, "url")
	if err != nil || !ok || v != "https://example.invalid/r.git" {
		t.Errorf("ConfigValue = %q, %v, %v", v, ok, err)
	}
}
