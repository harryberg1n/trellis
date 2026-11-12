package tree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

// placeInside commits a trellis.toml that places trees inside the primary and
// pushes it, so the primary and what a tree is cut from agree.
func placeInside(t *testing.T, f *fixture) {
	t.Helper()
	commitFile(t, f.primary, "trellis.toml", "[tree.new]\nplace = \"inside\"\n")
	testiso.Git(t, f.primary, "push", "-q", "origin", "main")
}

// excludeLines counts the /.trellis/ lines of the primary's exclude file.
func excludeLines(t *testing.T, primary string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(primary, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if l == "/.trellis/" {
			n++
		}
	}
	return n
}

// Feedback #65 (fanpyp, 2026-11-06): a tree beside the primary is outside the
// session's working directory, so none of the repository's nested CLAUDE.md
// files or path-scoped rules load for it.
func TestNew_PlaceInsidePutsTheTreeUnderThePrimaryAndKeepsThePrimaryClean(t *testing.T) {
	f := newFixture(t)
	placeInside(t, f)

	if err := New(f.env(f.primary), "feat/x", ""); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(f.primary, ".trellis", "trees", "feat--x")
	if got := testiso.Git(t, path, "rev-parse", "--abbrev-ref", "HEAD"); got != "feat/x" {
		t.Errorf("branch at %s = %q, want feat/x", path, got)
	}
	if got := f.out.String(); got != path+"\n" {
		t.Errorf("output = %q, want the path %q", got, path)
	}
	if got := testiso.Git(t, f.primary, "status", "--porcelain"); got != "" {
		t.Errorf("the primary is not clean with a tree inside it:\n%s", got)
	}
	if n := excludeLines(t, f.primary); n != 1 {
		t.Errorf("exclude file has %d /.trellis/ lines, want 1", n)
	}
}

func TestNew_PlaceInsideWritesTheExcludeLineOnce(t *testing.T) {
	f := newFixture(t)
	placeInside(t, f)
	for _, name := range []string{"one", "two"} {
		if err := New(f.env(f.primary), name, ""); err != nil {
			t.Fatal(err)
		}
	}
	if n := excludeLines(t, f.primary); n != 1 {
		t.Errorf("exclude file has %d /.trellis/ lines after two trees, want 1", n)
	}
}

func TestNew_PlaceInsideFromATreeStillLandsUnderThePrimary(t *testing.T) {
	f := newFixture(t)
	placeInside(t, f)
	if err := New(f.env(f.primary), "one", ""); err != nil {
		t.Fatal(err)
	}
	one := filepath.Join(f.primary, ".trellis", "trees", "one")

	if err := New(f.env(one), "two", ""); err != nil {
		t.Fatal(err)
	}

	if two := filepath.Join(f.primary, ".trellis", "trees", "two"); !exists(two) {
		t.Errorf("no tree at %s", two)
	}
}

func TestNew_AnExplicitPathWinsOverPlaceInside(t *testing.T) {
	f := newFixture(t)
	placeInside(t, f)
	path := f.primary + "--elsewhere"

	if err := New(f.env(f.primary), "feat", path); err != nil {
		t.Fatal(err)
	}

	dot := filepath.Join(f.primary, ".trellis")
	if !exists(path) || exists(dot) {
		t.Errorf("tree at %s: %v, %s made: %v; want the path given and no .trellis", path, exists(path), dot, exists(dot))
	}
}

func TestAdopt_PlaceInsidePutsTheTreeUnderThePrimary(t *testing.T) {
	f := newFixture(t)
	placeInside(t, f)
	testiso.Git(t, f.primary, "branch", "fix/guards")
	testiso.Git(t, f.primary, "push", "-q", "origin", "fix/guards")
	testiso.Git(t, f.primary, "branch", "-D", "fix/guards")

	if err := Adopt(f.env(f.primary), "fix/guards", ""); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(f.primary, ".trellis", "trees", "fix--guards")
	if got := f.out.String(); got != path+"\n" {
		t.Errorf("output = %q, want the path %q", got, path)
	}
	if got := testiso.Git(t, f.primary, "status", "--porcelain"); got != "" {
		t.Errorf("the primary is not clean with an adopted tree inside it:\n%s", got)
	}
}

func TestMerge_ATreeInsideThePrimaryIsMergedAndRemoved(t *testing.T) {
	f := newFixture(t)
	placeInside(t, f)
	if err := New(f.env(f.primary), "feat", ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.primary, ".trellis", "trees", "feat")
	commitFile(t, path, "f.txt", "f\n")

	if err := Merge(f.env(f.primary), "", Named("feat")); err != nil {
		t.Fatal(err)
	}

	if exists(path) {
		t.Errorf("the tree at %s is still there after its merge", path)
	}
	if got := testiso.Git(t, f.primary, "status", "--porcelain"); got != "" {
		t.Errorf("the primary is not clean after the merge:\n%s", got)
	}
}

func TestList_NamesAnOrphanInsideThePrimary(t *testing.T) {
	f := newFixture(t)
	ghost := filepath.Join(f.primary, ".trellis", "trees", "ghost")
	testiso.Write(t, filepath.Join(ghost, "left.txt"), "x\n")

	if err := List(f.env(f.primary), false); err != nil {
		t.Fatal(err)
	}

	got := rows(f.out.String())
	want := []string{"-", "-", "-", "-", "-", "orphan", ghost}
	if len(got) != 2 || strings.Join(got[1], "|") != strings.Join(want, "|") {
		t.Errorf("list:\n%s\nwant one orphan row %q", f.out.String(), want)
	}
}
