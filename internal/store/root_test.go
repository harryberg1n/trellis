package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestRoot_StateNeverInsideTheRepo(t *testing.T) {
	r := testiso.Repo(t)
	t.Setenv("TRELLIS_STATE", filepath.Join(t.TempDir(), "state"))
	root, err := Root(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, in := Within(r, RepoDir(root, RepoID("", r))); in {
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

func TestWithin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	cases := []struct {
		name, p, rel string
		ok           bool
	}{
		{"the root itself", root, ".", true},
		{"a file below", filepath.Join(root, "a", "b.txt"), filepath.Join("a", "b.txt"), true},
		{"a dotdot-named child", filepath.Join(root, "..x"), "..x", true},
		{"a sibling", root + "-other", "", false},
		{"the parent", filepath.Dir(root), "", false},
		{"above through dotdot", filepath.Join(root, "..", "elsewhere"), "", false},
	}
	for _, c := range cases {
		rel, ok := Within(root, c.p)
		if rel != c.rel || ok != c.ok {
			t.Errorf("%s: Within = %q, %v; want %q, %v", c.name, rel, ok, c.rel, c.ok)
		}
	}
}

func TestCanonical_ResolvesAnAliasAndAPathThatDoesNotExistYet(t *testing.T) {
	dir := Canonical(t.TempDir())
	alias := testiso.AltSpelling(t, dir)
	if got := Canonical(alias); got != dir {
		t.Errorf("Canonical(alias) = %q, want %q", got, dir)
	}
	if got, want := Canonical(filepath.Join(alias, "no", "such.txt")), filepath.Join(dir, "no", "such.txt"); got != want {
		t.Errorf("Canonical(alias/no/such.txt) = %q, want %q", got, want)
	}
}

func TestRoot_StateRootThroughAnotherSpellingOfTheRepoIsRefused(t *testing.T) {
	repo := Canonical(testiso.Repo(t))
	t.Setenv("TRELLIS_STATE", filepath.Join(testiso.AltSpelling(t, repo), "state"))
	if root, err := Root(repo); err == nil {
		t.Fatalf("Root = %s, want an error: the state root is inside the repo under another spelling", root)
	}
}

func TestRoot_WithoutARepoSkipsTheContainmentCheck(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(wd, "state")
	t.Setenv("TRELLIS_STATE", state)
	got, err := Root("")
	if err != nil || got != state {
		t.Fatalf("Root(\"\") = %q, %v; want %q: with no repo there is nothing to be inside of", got, err, state)
	}
}
