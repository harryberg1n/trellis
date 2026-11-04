package tree

import (
	"slices"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

// Issue #53: `tree merge` ran inside the tree and removed it, so the caller's
// shell was left in a deleted directory and the agent's harness reported the
// landed merge as exit 1 (22 of about 30 merges). Named from the primary, the
// caller stands nowhere that is removed.
func TestMerge_FromThePrimaryByName(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")

	if err := Merge(f.env(f.primary), "", Named("feat")); err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(f.kinds(), "tree.merged") || exists(path) {
		t.Errorf("kinds %v, tree exists %v; want merged and removed", f.kinds(), exists(path))
	}
	if strings.Contains(f.out.String(), "your shell is still in") {
		t.Errorf("output = %q, want no cd hint: the caller is in the primary", f.out.String())
	}
	if got := testiso.Git(t, f.origin, "branch", "--list", "feat"); got != "" {
		t.Errorf("origin still has feat (%q)", got)
	}
}

func TestMerge_FromAnotherTreeByName(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	if err := New(f.env(f.primary), "other", ""); err != nil {
		t.Fatal(err)
	}

	if err := Merge(f.env(f.primary+"--other"), "", Named("feat")); err != nil {
		t.Fatal(err)
	}

	if exists(path) || !exists(f.primary+"--other") {
		t.Errorf("feat exists %v, other exists %v; want feat removed and other kept", exists(path), exists(f.primary+"--other"))
	}
}

func TestMerge_FromThePrimaryWithoutANameSaysHow(t *testing.T) {
	f := newFixture(t)
	f.openTree(t)

	err := Merge(f.env(f.primary), "")

	want := "tree merge: main is the default branch and the primary checkout is not a tree: name one, trellis tree merge <name>"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestMerge_ANameNoTreeIsOnIsRefused(t *testing.T) {
	f := newFixture(t)
	f.openTree(t)

	err := Merge(f.env(f.primary), "", Named("nope"))

	want := `tree merge: no tree is on branch "nope"`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if len(f.gh.Specs()) != 0 {
		t.Errorf("gh calls = %v, want none", f.ghCalls())
	}
}

func TestMerge_TheDefaultBranchByNameIsRefused(t *testing.T) {
	f := newFixture(t)

	err := Merge(f.env(f.primary), "", Named("main"))

	want := "tree merge: main is the default branch"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestMerge_LocalFromThePrimaryByName(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")

	if err := Merge(f.env(f.primary), "exit 0", Named("feat")); err != nil {
		t.Fatal(err)
	}

	if got := testiso.Git(t, f.primary, "log", "-1", "--format=%s"); got != "Merge tree feat" {
		t.Errorf("subject = %q, want Merge tree feat", got)
	}
	if exists(path) {
		t.Errorf("tree still exists")
	}
}
