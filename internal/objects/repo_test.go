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
	want := "detached-" + testsupport.Git(t, repo, "rev-parse", "--short=12", "HEAD")
	if got, _ := Branch(git); got != want || len(got) != len("detached-")+12 {
		t.Errorf("Branch = %q, want %q", got, want)
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

func TestFindRoot_HonoursCeilingDirectories(t *testing.T) {
	repo := testiso.Repo(t)
	sub := filepath.Join(repo, "sub")
	testsupport.Write(t, filepath.Join(sub, "f"), "x")
	t.Setenv("GIT_CEILING_DIRECTORIES", repo)
	if _, err := FindRoot(sub); !errors.Is(err, ErrNotARepo) {
		t.Errorf("FindRoot below the ceiling: %v, want ErrNotARepo", err)
	}
	if got, err := FindRoot(repo); err != nil || got != repo {
		t.Errorf("FindRoot at the ceiling = %q, %v; want %q", got, err, repo)
	}
}

func TestFindRoot_NeedsAHeadFile(t *testing.T) {
	repo := testiso.Repo(t)
	hollow := filepath.Join(repo, "hollow")
	testsupport.Write(t, filepath.Join(hollow, ".git", "config"), "")
	got, err := FindRoot(hollow)
	if err != nil || got != repo {
		t.Errorf("FindRoot = %q, %v; want the enclosing repo %q (a .git without HEAD is not one)", got, err, repo)
	}
}

func TestDefaultBranch(t *testing.T) {
	repo := testiso.Repo(t)
	common, _, err := GitDirs(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := DefaultBranch(common); got != "main" {
		t.Errorf("DefaultBranch with no origin/HEAD = %q, want the fallback main", got)
	}
	testsupport.Write(t, filepath.Join(common, "refs", "remotes", "origin", "HEAD"), "ref: refs/remotes/origin/trunk\n")
	if got := DefaultBranch(common); got != "trunk" {
		t.Errorf("DefaultBranch = %q, want trunk", got)
	}
	testsupport.Write(t, filepath.Join(common, "refs", "remotes", "origin", "HEAD"), "ref: refs/remotes/origin/release/2\n")
	if got := DefaultBranch(common); got != "release/2" {
		t.Errorf("DefaultBranch = %q, want release/2", got)
	}
	testsupport.Write(t, filepath.Join(common, "refs", "remotes", "origin", "HEAD"), "garbage\n")
	if got := DefaultBranch(common); got != "main" {
		t.Errorf("DefaultBranch on a malformed origin/HEAD = %q, want main", got)
	}
}

func TestIsPrimary(t *testing.T) {
	repo := testiso.Repo(t)
	if got, err := IsPrimary(repo); err != nil || !got {
		t.Errorf("IsPrimary(repo) = %v, %v; want true", got, err)
	}
	lane := filepath.Join(filepath.Dir(repo), "lane")
	testsupport.Git(t, repo, "worktree", "add", "-q", "-b", "lane/x", lane)
	if got, err := IsPrimary(lane); err != nil || got {
		t.Errorf("IsPrimary(linked worktree) = %v, %v; want false", got, err)
	}
}
