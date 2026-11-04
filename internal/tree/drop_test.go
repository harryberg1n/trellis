package tree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/testiso"
)

// Owner, 2026-11-04: an agent could not drop a lane that was worthless but
// not merged. `git branch -D` and `git worktree remove --force` are refused by
// discard-uncommitted, its override is for a person, and `tree rm --force`
// discards for good. `tree drop` keeps everything under refs/trellis/, so
// nothing is lost and nothing needs an override.

const dropRef = "refs/trellis/dropped/feat-19700101T000000Z" // the instant clock reads the Unix epoch

func TestDrop_KeepsCommitsAndUncommittedWorkUnderATrellisRef(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	tip := testiso.Git(t, path, "rev-parse", "HEAD")
	testiso.Write(t, filepath.Join(path, "mine.txt"), "changed\n")
	testiso.Write(t, filepath.Join(path, "new.txt"), "untracked\n")

	if err := Drop(f.env(f.primary), "feat", "duplicate of #12"); err != nil {
		t.Fatal(err)
	}

	if exists(path) {
		t.Errorf("tree still exists")
	}
	if got := testiso.Git(t, f.primary, "branch", "--list", "feat"); got != "" {
		t.Errorf("branch feat still exists (%q)", got)
	}
	if got := testiso.Git(t, f.primary, "rev-parse", dropRef+"^"); got != tip {
		t.Errorf("parent of the kept commit = %s, want the branch tip %s", got, tip)
	}
	if got := testiso.Git(t, f.primary, "show", dropRef+":mine.txt"); got != "changed" {
		t.Errorf("mine.txt kept as %q, want the uncommitted change", got)
	}
	if got := testiso.Git(t, f.primary, "show", dropRef+":new.txt"); got != "untracked" {
		t.Errorf("new.txt kept as %q, want the untracked file", got)
	}
	want := "dropped feat: kept at " + dropRef + " (" + testiso.Git(t, f.primary, "rev-parse", dropRef)[:12] + ")\n" +
		"restore: git worktree add " + path + " -b feat " + dropRef + "\n"
	if !strings.HasPrefix(f.out.String(), want) {
		t.Errorf("output = %q, want it to start with %q", f.out.String(), want)
	}
	if len(f.events) != 1 || f.events[0].Kind != kernel.EvTreeDropped || f.events[0].Tree != "feat" ||
		f.events[0].Detail["reason"] != "duplicate of #12" || f.events[0].Detail["ref"] != dropRef {
		t.Errorf("events = %+v, want one tree.dropped with the reason and the ref", f.events)
	}
}

func TestDrop_ACleanTreeKeepsItsTipAsItIs(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	tip := testiso.Git(t, path, "rev-parse", "HEAD")

	if err := Drop(f.env(f.primary), "feat", "superseded"); err != nil {
		t.Fatal(err)
	}

	if got := testiso.Git(t, f.primary, "rev-parse", dropRef); got != tip {
		t.Errorf("kept %s, want the tip %s itself", got, tip)
	}
}

func TestDrop_TheTreeTheCallerStandsIn(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")

	if err := Drop(f.env(path), "", "wrong approach"); err != nil {
		t.Fatal(err)
	}

	if exists(path) {
		t.Errorf("tree still exists")
	}
	if !strings.Contains(f.out.String(), "your shell is still in "+path+": cd "+f.primary+"\n") {
		t.Errorf("output = %q, want the cd hint", f.out.String())
	}
}

func TestDrop_ADropOfTheSameNameInTheSameSecondKeepsBoth(t *testing.T) {
	f := newFixture(t)
	f.openTree(t)
	if err := Drop(f.env(f.primary), "feat", "first"); err != nil {
		t.Fatal(err)
	}
	f.openTree(t)

	if err := Drop(f.env(f.primary), "feat", "second"); err != nil {
		t.Fatal(err)
	}

	testiso.Git(t, f.primary, "rev-parse", "--verify", dropRef)
	testiso.Git(t, f.primary, "rev-parse", "--verify", dropRef+"-2")
}

func TestDrop_Refusals(t *testing.T) {
	f := newFixture(t)
	f.openTree(t)
	for _, c := range []struct {
		dir, name, reason, want string
	}{
		{f.primary, "feat", "", "tree drop: --reason is needed: say why the work is dropped"},
		{f.primary, "feat", "  ", "tree drop: --reason is needed: say why the work is dropped"},
		{f.primary, "", "x", "tree drop: the primary checkout is not a tree: name one, trellis tree drop <name> --reason <why>"},
		{f.primary, "main", "x", "tree drop: that is the primary checkout, not a tree"},
		{f.primary, "nope", "x", `tree drop: no tree is on branch "nope"`},
	} {
		err := Drop(f.env(c.dir), c.name, c.reason)
		if err == nil || err.Error() != c.want {
			t.Errorf("Drop(%q, %q): err = %v, want %q", c.name, c.reason, err, c.want)
		}
	}
	if len(f.events) != 0 {
		t.Errorf("events = %+v, want none", f.events)
	}
}

func TestRm_TheUnmergedRefusalNamesDrop(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")

	err := Rm(f.env(f.primary), "feat", false)

	want := "tree rm: feat has 1 commit not merged into origin/main; trellis tree drop feat --reason <why> keeps it recoverable, --force discards it"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestRm_TheDirtyRefusalNamesDrop(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	testiso.Write(t, filepath.Join(path, "new.txt"), "x\n")

	err := Rm(f.env(f.primary), "feat", false)

	want := "tree rm: feat has 1 untracked file; trellis tree drop feat --reason <why> keeps it recoverable, --force discards it"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// Issue #52 item 4: a peer pushed fix/worker-guards and asked this host to
// sync and merge it; `tree new` refused a name already on origin and the tree
// was made by hand.
func TestAdopt_MakesATreeOfABranchOnOrigin(t *testing.T) {
	f := newFixture(t)
	testiso.Git(t, f.primary, "branch", "fix/worker-guards")
	testiso.Git(t, f.primary, "push", "-q", "origin", "fix/worker-guards")
	testiso.Git(t, f.primary, "branch", "-D", "fix/worker-guards")

	if err := Adopt(f.env(f.primary), "fix/worker-guards", ""); err != nil {
		t.Fatal(err)
	}

	path := f.primary + "--fix--worker-guards"
	if got := testiso.Git(t, path, "rev-parse", "--abbrev-ref", "HEAD@{upstream}"); got != "origin/fix/worker-guards" {
		t.Errorf("upstream = %q, want origin/fix/worker-guards", got)
	}
	if f.out.String() != path+"\n" {
		t.Errorf("output = %q, want the path", f.out.String())
	}
	if len(f.events) != 1 || f.events[0].Kind != kernel.EvTreeOpened || f.events[0].Tree != "fix/worker-guards" {
		t.Errorf("events = %+v, want one tree.opened", f.events)
	}
}

func TestAdopt_ALocalBranchWithoutATree(t *testing.T) {
	f := newLocalFixture(t)
	testiso.Git(t, f.primary, "branch", "peer")

	if err := Adopt(f.env(f.primary), "peer", ""); err != nil {
		t.Fatal(err)
	}

	if got := testiso.Git(t, f.primary+"--peer", "branch", "--show-current"); got != "peer" {
		t.Errorf("tree is on %q, want peer", got)
	}
}

func TestAdopt_Refusals(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	for _, c := range []struct{ name, want string }{
		{"feat", "tree adopt: feat is already a tree at " + path},
		{"main", "tree adopt: main is the default branch"},
		{"nope", `tree adopt: no branch "nope" on origin`},
	} {
		err := Adopt(f.env(f.primary), c.name, "")
		if err == nil || err.Error() != c.want {
			t.Errorf("Adopt(%q): err = %v, want %q", c.name, err, c.want)
		}
	}
	if _, err := os.Stat(f.primary + "--nope"); err == nil {
		t.Errorf("a tree was made for nope")
	}
}
