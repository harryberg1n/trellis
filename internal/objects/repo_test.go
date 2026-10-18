package objects

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestBranch(t *testing.T) {
	repo := testiso.Repo(t)
	git := filepath.Join(repo, ".git")
	if got, err := Branch(git); err != nil || got != "main" {
		t.Errorf("Branch = %q, %v; want main", got, err)
	}
	testiso.Git(t, repo, "checkout", "-q", "-b", "feature/deep/x")
	if got, _ := Branch(git); got != "feature/deep/x" {
		t.Errorf("Branch = %q, want feature/deep/x", got)
	}
	testiso.Git(t, repo, "checkout", "-q", "--detach")
	want := "detached-" + testiso.Git(t, repo, "rev-parse", "--short=12", "HEAD")
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
	testiso.Git(t, repo, "remote", "add", "origin", "https://example.invalid/r.git")
	v, ok, err := ConfigValue(filepath.Join(repo, ".git"), `remote "origin"`, "url")
	if err != nil || !ok || v != "https://example.invalid/r.git" {
		t.Errorf("ConfigValue = %q, %v, %v", v, ok, err)
	}
}

func TestFindRoot_HonoursCeilingDirectories(t *testing.T) {
	repo := testiso.Repo(t)
	sub := filepath.Join(repo, "sub")
	testiso.Write(t, filepath.Join(sub, "f"), "x")
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
	testiso.Write(t, filepath.Join(hollow, ".git", "config"), "")
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
	testiso.Write(t, filepath.Join(common, "refs", "remotes", "origin", "HEAD"), "ref: refs/remotes/origin/trunk\n")
	if got := DefaultBranch(common); got != "trunk" {
		t.Errorf("DefaultBranch = %q, want trunk", got)
	}
	testiso.Write(t, filepath.Join(common, "refs", "remotes", "origin", "HEAD"), "ref: refs/remotes/origin/release/2\n")
	if got := DefaultBranch(common); got != "release/2" {
		t.Errorf("DefaultBranch = %q, want release/2", got)
	}
	testiso.Write(t, filepath.Join(common, "refs", "remotes", "origin", "HEAD"), "garbage\n")
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
	testiso.Git(t, repo, "worktree", "add", "-q", "-b", "lane/x", lane)
	if got, err := IsPrimary(lane); err != nil || got {
		t.Errorf("IsPrimary(linked worktree) = %v, %v; want false", got, err)
	}
}

func TestDefaultBranch_WithoutOriginHEADReadsConfigThenTheBranchThatExists(t *testing.T) {
	repo := testiso.Repo(t)
	common, _, err := GitDirs(repo)
	if err != nil {
		t.Fatal(err)
	}
	testiso.Git(t, repo, "branch", "-m", "master")
	if got := DefaultBranch(common); got != "master" {
		t.Errorf("repo whose only branch is master: DefaultBranch = %q, want master", got)
	}
	testiso.Git(t, repo, "branch", "main")
	if got := DefaultBranch(common); got != "main" {
		t.Errorf("both main and master: DefaultBranch = %q, want main", got)
	}
	testiso.Git(t, repo, "branch", "trunk")
	testiso.Git(t, repo, "config", "init.defaultBranch", "trunk")
	if got := DefaultBranch(common); got != "trunk" {
		t.Errorf("init.defaultBranch=trunk with a trunk branch: DefaultBranch = %q, want trunk", got)
	}
	testiso.Git(t, repo, "config", "init.defaultBranch", "gone")
	if got := DefaultBranch(common); got != "main" {
		t.Errorf("init.defaultBranch names no branch: DefaultBranch = %q, want main", got)
	}
}

func TestOpen_PrimaryAndLinkedWorktree(t *testing.T) {
	repo := testiso.Repo(t)
	testiso.Git(t, repo, "config", "remote.origin.url", "https://example.invalid/o/r.git")
	lane := filepath.Join(filepath.Dir(repo), "lane")
	testiso.Git(t, repo, "worktree", "add", "-q", "-b", "lane/x", lane)

	got, err := Open(filepath.Join(repo, "no", "such", "dir"))
	if err != nil {
		t.Fatal(err)
	}
	want := Repo{
		Root: repo, Common: filepath.Join(repo, ".git"), GitDir: filepath.Join(repo, ".git"),
		Branch: "main", Primary: true, Remote: "https://example.invalid/o/r.git", Default: "main",
	}
	if got != want {
		t.Errorf("Open(primary) = %+v, want %+v", got, want)
	}

	got, err = Open(lane)
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != lane || got.Branch != "lane/x" || got.Primary || got.Common != want.Common ||
		got.GitDir == got.Common || got.Remote != want.Remote {
		t.Errorf("Open(lane) = %+v", got)
	}
	if _, err := Open(filepath.Dir(repo)); !errors.Is(err, ErrNotARepo) {
		t.Errorf("Open outside a repo: %v, want ErrNotARepo", err)
	}
}
