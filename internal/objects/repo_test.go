package objects

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestBranch(t *testing.T) {
	repo := testiso.Repo(t)
	git := filepath.Join(repo, ".git")
	if got, detached, err := Branch(git); err != nil || got != "main" || detached {
		t.Errorf("Branch = %q, detached %v, %v; want main, attached", got, detached, err)
	}
	testiso.Git(t, repo, "checkout", "-q", "-b", "feature/deep/x")
	if got, _, _ := Branch(git); got != "feature/deep/x" {
		t.Errorf("Branch = %q, want feature/deep/x", got)
	}
	testiso.Git(t, repo, "checkout", "-q", "--detach")
	want := "detached-" + testiso.Git(t, repo, "rev-parse", "--short=12", "HEAD")
	if got, detached, _ := Branch(git); got != want || !detached || len(got) != len("detached-")+12 {
		t.Errorf("Branch = %q, detached %v, want %q and detached", got, detached, want)
	}
	// a real branch that happens to be called detached-<something> is not detached
	testiso.Git(t, repo, "checkout", "-q", "-b", "detached-fix")
	if got, detached, _ := Branch(git); got != "detached-fix" || detached {
		t.Errorf("Branch = %q, detached %v; want the branch detached-fix, attached", got, detached)
	}
	if r, err := Open(repo); err != nil || r.Detached || r.Branch != "detached-fix" {
		t.Errorf("Open = %+v, %v; want branch detached-fix, not detached", r, err)
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
	tree := testiso.AddTree(t, repo, "tree", "tree/x")
	if got, err := IsPrimary(tree); err != nil || got {
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
	repo := store.Canonical(testiso.Repo(t))
	testiso.Git(t, repo, "config", "remote.origin.url", "https://example.invalid/o/r.git")
	tree := store.Canonical(testiso.AddTree(t, repo, "tree", "tree/x"))

	got, err := Open(filepath.Join(repo, "no", "such", "dir"))
	if err != nil {
		t.Fatal(err)
	}
	want := Repo{
		Root: repo, Common: filepath.Join(repo, ".git"), GitDir: filepath.Join(repo, ".git"),
		Branch: "main", Primary: true, Remote: "https://example.invalid/o/r.git", Default: "main", History: "refs/heads/main",
	}
	if got != want {
		t.Errorf("Open(primary) = %+v, want %+v", got, want)
	}

	got, err = Open(tree)
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != tree || got.Branch != "tree/x" || got.Primary || got.Common != want.Common ||
		got.GitDir == got.Common || got.Remote != want.Remote {
		t.Errorf("Open(tree) = %+v", got)
	}
	if _, err := Open(filepath.Dir(repo)); !errors.Is(err, ErrNotARepo) {
		t.Errorf("Open outside a repo: %v, want ErrNotARepo", err)
	}
}

func TestOpen_PathsAreCanonicalWhateverSpellingTheCallerUses(t *testing.T) {
	repo := store.Canonical(testiso.Repo(t))
	got, err := Open(testiso.AltSpelling(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != repo || got.Common != filepath.Join(repo, ".git") || got.GitDir != got.Common {
		t.Errorf("Open(alias) root %q common %q gitdir %q, want %q and its .git", got.Root, got.Common, got.GitDir, repo)
	}
}

func TestOpen_AnUnreadableRemoteDoesNotMakeTheRepoUnknown(t *testing.T) {
	repo := testiso.Repo(t)
	cfg := filepath.Join(repo, ".git", "config")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A line over bufio.Scanner's 64KB limit makes the config read fail.
	_, werr := f.WriteString("[remote \"origin\"]\n\turl = " + strings.Repeat("x", 70000) + "\n")
	if cerr := f.Close(); werr != nil || cerr != nil {
		t.Fatal(werr, cerr)
	}
	got, err := Open(repo)
	if err != nil || got.Branch != "main" || got.Remote != "" {
		t.Errorf("Open = %+v, %v; want the repo on main with no remote", got, err)
	}
}

func TestHistoryRef_PrefersTheRemoteThenTheLocalBranchThenHEAD(t *testing.T) {
	repo := testiso.Repo(t)
	common, _, err := GitDirs(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := HistoryRef(common); got != "refs/heads/main" {
		t.Errorf("local main only: HistoryRef = %q, want refs/heads/main", got)
	}
	testiso.Git(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	if got := HistoryRef(common); got != "refs/remotes/origin/main" {
		t.Errorf("origin/main exists: HistoryRef = %q, want refs/remotes/origin/main", got)
	}
	testiso.Git(t, repo, "update-ref", "-d", "refs/remotes/origin/main")
	testiso.Git(t, repo, "branch", "-m", "trunk")
	if got := HistoryRef(common); got != "HEAD" {
		t.Errorf("no main or master: HistoryRef = %q, want HEAD", got)
	}
}

func TestPrimaryRoot_ALinkedWorktreeNamesItsPrimaryCheckout(t *testing.T) {
	repo := testiso.Repo(t)
	tree := testiso.AddTree(t, repo, "tree", "tree/x")

	inPrimary, err := Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	inTree, err := Open(tree)
	if err != nil {
		t.Fatal(err)
	}
	if got := inPrimary.PrimaryRoot(); got != repo {
		t.Errorf("PrimaryRoot in the primary = %q, want %q", got, repo)
	}
	if got := inTree.PrimaryRoot(); got != repo {
		t.Errorf("PrimaryRoot in a linked worktree = %q, want the primary %q", got, repo)
	}
}

func TestHasMarker_IsTheCommittedFileAtTheRoot(t *testing.T) {
	repo := testiso.Repo(t)
	if HasMarker(repo) {
		t.Errorf("HasMarker without trellis.toml = true, want false")
	}
	testiso.Write(t, filepath.Join(repo, "trellis.toml"), "# on\n")
	if !HasMarker(repo) {
		t.Errorf("HasMarker with trellis.toml = false, want true")
	}
}

func TestHasMarker_ADirectoryNamedTrellisTomlIsNotTheMarker(t *testing.T) {
	repo := testiso.Repo(t)
	if err := os.Mkdir(filepath.Join(repo, "trellis.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if HasMarker(repo) {
		t.Errorf("HasMarker with a directory called trellis.toml = true, want false")
	}
}

// A rebase detaches HEAD and names the branch it rebuilds in head-name, under
// the worktree's own git dir: .git/worktrees/<name>/ for a linked worktree,
// never the common dir.
func TestBranch_ARebaseInAWorktreeKeepsItsBranch(t *testing.T) {
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		t.Run(dir, func(t *testing.T) {
			primary := testiso.Repo(t)
			tree := testiso.AddTree(t, primary, "tree", "tree/x")
			testiso.Git(t, tree, "checkout", "-q", "--detach")
			_, gitDir, err := GitDirs(tree)
			if err != nil || filepath.Base(filepath.Dir(gitDir)) != "worktrees" {
				t.Fatalf("GitDirs(tree) = %q, %v; want a dir under worktrees/", gitDir, err)
			}
			// A head-name in the common dir belongs to the primary's rebase, not this one.
			testiso.Write(t, filepath.Join(primary, ".git", dir, "head-name"), "refs/heads/primary-rebase\n")
			if r, err := Open(tree); err != nil || r.Branch == "primary-rebase" || !strings.HasPrefix(r.Branch, "detached-") {
				t.Fatalf("Open with the head-name in the common dir only: branch %q, %v; want detached-<sha>", r.Branch, err)
			}
			testiso.Write(t, filepath.Join(gitDir, dir, "head-name"), "refs/heads/lane/rebased\n")
			r, err := Open(tree)
			if err != nil || r.Branch != "lane/rebased" || !r.Detached {
				t.Errorf("Open = branch %q, detached %v, %v; want lane/rebased, still detached", r.Branch, r.Detached, err)
			}
		})
	}
}

func TestStashEmpty_FollowsTheStashRefAndItsLog(t *testing.T) {
	repo := testiso.Repo(t)
	common := filepath.Join(repo, ".git")
	if !StashEmpty(common) {
		t.Fatal("StashEmpty of a repository that never stashed = false, want true")
	}
	testiso.Write(t, filepath.Join(repo, "README.md"), "changed\n")
	testiso.Git(t, repo, "stash", "push", "-q")
	if StashEmpty(common) {
		t.Error("StashEmpty with one entry = true, want false")
	}
	testiso.Write(t, filepath.Join(repo, "README.md"), "changed again\n")
	testiso.Git(t, repo, "stash", "push", "-q")
	testiso.Git(t, repo, "stash", "drop", "-q")
	if StashEmpty(common) {
		t.Error("StashEmpty with one of two entries dropped = true, want false")
	}
	testiso.Git(t, repo, "stash", "clear")
	if !StashEmpty(common) {
		t.Error("StashEmpty after stash clear = false, want true")
	}
	// A log left behind with no entries in it is empty; one with an entry is not.
	testiso.Write(t, filepath.Join(common, "logs", "refs", "stash"), "")
	if !StashEmpty(common) {
		t.Error("StashEmpty with an empty stash log = false, want true")
	}
	testiso.Write(t, filepath.Join(common, "logs", "refs", "stash"), "0000 1111 A <a@b> 1 +0000\tWIP\n")
	if StashEmpty(common) {
		t.Error("StashEmpty with an entry in the stash log = true, want false")
	}
}

func TestStashEmpty_AnUnreadableRefIsNotEmpty(t *testing.T) {
	repo := testiso.Repo(t)
	common := filepath.Join(repo, ".git")
	testiso.Write(t, filepath.Join(common, "refs", "stash"), "ref: refs/heads/main\n")
	if StashEmpty(common) {
		t.Error("StashEmpty with a refs/stash it cannot read = true, want false (unknown is not empty)")
	}
}
