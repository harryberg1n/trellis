package tree

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/testiso"
)

// Issue #50 item 2 (fanpyp, 2026-10-25): 41 worktrees (17 GB) and 13 orphan
// directories; `tree rm` removed one trellis tree at a time and nothing said
// which were safe to drop, so the cleanup was scripted.

// listWorld is a primary with four trees and an orphan directory:
// merged (its PR merged with its tip, clean), dirty (an untracked file),
// open (an open PR), gone (its directory deleted by hand).
type listWorld struct {
	f                         *fixture
	merged, dirty, open, gone string // paths
	mergedTip                 string
	orphan                    string
}

func newListWorld(t *testing.T) listWorld {
	t.Helper()
	f := newFixture(t)
	w := listWorld{f: f}
	for _, name := range []string{"merged", "dirty", "open", "gone"} {
		if err := New(f.env(f.primary), name, ""); err != nil {
			t.Fatal(err)
		}
	}
	w.merged, w.dirty, w.open, w.gone = f.primary+"--merged", f.primary+"--dirty", f.primary+"--open", f.primary+"--gone"
	commitFile(t, w.merged, "m.txt", "m\n")
	w.mergedTip = testiso.Git(t, w.merged, "rev-parse", "HEAD")
	testiso.Write(t, filepath.Join(w.dirty, "new.txt"), "n\n")
	commitFile(t, w.open, "o.txt", "o\n")
	if err := os.RemoveAll(w.gone); err != nil {
		t.Fatal(err)
	}
	w.orphan = f.primary + "--ghost"
	testiso.Write(t, filepath.Join(w.orphan, "left.txt"), "x\n")
	f.prStates = map[string]string{"merged": "MERGED", "open": "OPEN"}
	f.mergedTips = map[string]string{"merged": w.mergedTip}
	ct, err := strconv.ParseInt(testiso.Git(t, w.merged, "log", "-1", "--format=%ct"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	f.clk.now = time.Unix(ct, 0).Add(73 * time.Hour) // every commit of the test is a few seconds old: 3d
	f.events = nil
	f.out.Reset()
	return w
}

// rows are the output's lines split into fields.
func rows(out string) [][]string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	rs := make([][]string, 0, len(lines))
	for _, l := range lines {
		rs = append(rs, strings.Fields(l))
	}
	return rs
}

func TestList_ShowsEveryTreeAndWhatPruneWouldDo(t *testing.T) {
	w := newListWorld(t)
	f := w.f

	if err := List(f.env(f.primary), false); err != nil {
		t.Fatal(err)
	}

	want := [][]string{
		{"TREE", "AHEAD", "FILES", "PR", "AGE", "PRUNE", "PATH"},
		{"dirty", "0", "1", "none", "3d", "keep(uncommitted)", w.dirty},
		{"gone", "0", "-", "none", "3d", "prune(missing)", w.gone},
		{"merged", "1", "0", "merged", "3d", "remove", w.merged},
		{"open", "1", "0", "open", "3d", "keep(open-pr)", w.open},
		{"-", "-", "-", "-", "-", "orphan", w.orphan},
	}
	got := rows(f.out.String())
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("list:\n%s\nwant rows %q", f.out.String(), want)
	}
}

func TestPrune_RemovesMergedCleanTreesAndMissingOnes(t *testing.T) {
	w := newListWorld(t)
	f := w.f

	if err := Prune(f.env(f.primary), false, false); err != nil {
		t.Fatal(err)
	}

	if exists(w.merged) || testiso.Git(t, f.primary, "branch", "--list", "merged") != "" {
		t.Errorf("merged tree or branch still exists")
	}
	if !exists(w.dirty) || !exists(w.open) || !exists(w.orphan) {
		t.Errorf("dirty %v, open %v, orphan %v; want all kept", exists(w.dirty), exists(w.open), exists(w.orphan))
	}
	if strings.Contains(testiso.Git(t, f.primary, "worktree", "list", "--porcelain"), w.gone) {
		t.Errorf("the missing tree is still listed by git")
	}
	for _, line := range []string{
		"removed " + w.merged + "\n",
		"pruned gone: its directory was gone\n",
		"kept dirty: uncommitted\n",
		"kept open: open-pr\n",
		"orphan " + w.orphan + ": not a worktree of this repository; remove it by hand if it holds nothing\n",
	} {
		if !strings.Contains(f.out.String(), line) {
			t.Errorf("output = %q, want the line %q", f.out.String(), line)
		}
	}
}

func TestPrune_DryChangesNothing(t *testing.T) {
	w := newListWorld(t)
	f := w.f

	if err := Prune(f.env(f.primary), true, false); err != nil {
		t.Fatal(err)
	}

	if !exists(w.merged) || !strings.Contains(testiso.Git(t, f.primary, "worktree", "list", "--porcelain"), filepath.ToSlash(w.gone)) {
		t.Errorf("dry run changed something")
	}
	if !strings.Contains(f.out.String(), "would remove merged "+w.merged+"\n") || !strings.Contains(f.out.String(), "would prune gone\n") {
		t.Errorf("output = %q, want the plan", f.out.String())
	}
}

func TestPrune_BackupKeepsAndRemovesEveryOtherTree(t *testing.T) {
	w := newListWorld(t)
	f := w.f

	if err := Prune(f.env(f.primary), false, true); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{w.merged, w.dirty, w.open} {
		if exists(p) {
			t.Errorf("%s still exists", p)
		}
	}
	stamp := f.clk.now.UTC().Format("20060102T150405Z")
	if got := testiso.Git(t, f.primary, "show", "refs/trellis/dropped/dirty-"+stamp+":new.txt"); got != "n" {
		t.Errorf("dirty's untracked file kept as %q, want n", got)
	}
	if got := testiso.Git(t, f.origin, "rev-parse", "backup/open"); got != testiso.Git(t, f.primary, "rev-parse", "refs/trellis/dropped/open-"+stamp) {
		t.Errorf("origin backup/open = %s, want the kept commit", got)
	}
	var dropped []string
	for _, ev := range f.events {
		if ev.Kind == kernel.EvTreeDropped {
			dropped = append(dropped, ev.Tree+":"+ev.Detail["reason"])
		}
	}
	if want := []string{"dirty:tree prune --backup", "open:tree prune --backup"}; !slices.Equal(dropped, want) {
		t.Errorf("dropped events = %v, want %v", dropped, want)
	}
}

func TestPrune_KeepsTheTreeTheCallerStandsIn(t *testing.T) {
	w := newListWorld(t)
	f := w.f

	if err := Prune(f.env(w.merged), false, true); err != nil {
		t.Fatal(err)
	}

	if !exists(w.merged) {
		t.Errorf("the caller's tree was removed")
	}
	if !strings.Contains(f.out.String(), "kept merged: you are in it\n") {
		t.Errorf("output = %q, want the kept line", f.out.String())
	}
}
