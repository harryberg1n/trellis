package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

func under(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func TestRoot_StateNeverInsideTheRepo(t *testing.T) {
	r := testiso.Repo(t)
	t.Setenv("TRELLIS_STATE", filepath.Join(t.TempDir(), "state"))
	root, err := Root(r)
	if err != nil {
		t.Fatal(err)
	}
	if under(RepoDir(root, RepoID("", r)), r) {
		t.Fatalf("state %s is under the repo %s", root, r)
	}
}

func TestRoot_OverrideInsideTheRepoIsRefused(t *testing.T) {
	r := testiso.Repo(t)
	t.Setenv("TRELLIS_STATE", filepath.Join(r, ".trellis"))
	if root, err := Root(r); err == nil {
		t.Fatalf("Root = %s, want an error", root)
	}
}

func TestRoot_OverrideWins(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	t.Setenv("TRELLIS_STATE", dir)
	got, err := Root(filepath.Join(t.TempDir(), "repo"))
	if err != nil || got != dir {
		t.Fatalf("Root = %q, %v; want %q", got, err, dir)
	}
}

func TestRepoID_IsSixteenHexOfSha256(t *testing.T) {
	// sha256("abc") = ba7816bf8f01cfea414140de5dae2223...
	if got := RepoID("abc", "/ignored"); got != "ba7816bf8f01cfea" {
		t.Fatalf("RepoID = %q", got)
	}
}

func TestRepoID_FallsBackToThePath(t *testing.T) {
	if RepoID("", "abc") != "ba7816bf8f01cfea" {
		t.Fatal("empty remote must hash the path")
	}
}

func TestRepoDir_JoinsReposAndID(t *testing.T) {
	want := filepath.Join("R", "repos", "ba7816bf8f01cfea")
	if got := RepoDir("R", "ba7816bf8f01cfea"); got != want {
		t.Fatalf("RepoDir = %q, want %q", got, want)
	}
}
