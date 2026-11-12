package walls_test

import (
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/testiso"
)

// Issues #50 and #52: primary-write (30 denies) and discard-uncommitted on
// `checkout -- <paths>` (36 denies) were all unresolved in the reports. The
// deny said what was refused, not what to do instead, and the way out an
// agent found (`git show HEAD:<path> > <path>`) discards the same work with no
// wall in its path. Each deny now names the step that keeps the work.

func denyLineOf(t *testing.T, payload []byte) string {
	t.Helper()
	d := verdicts(decide(t, payload, quiet(t)), kernel.VerdictDeny)
	if len(d) != 1 {
		t.Fatalf("denies %+v, want one", d)
	}
	return d[0].Line
}

func TestDiscard_EachDenyNamesTheStepThatKeepsTheWork(t *testing.T) {
	w := newWorld(t)
	for _, c := range []struct{ cmd, want string }{
		{"git checkout -- README.md", "trellis: discard-uncommitted: checkout -- <paths> discards 1 unstaged file: git stash push -- README.md sets it aside instead. Override: trellis allow discard-uncommitted."},
		{"git restore README.md scratch.txt", "trellis: discard-uncommitted: restore <paths> discards 1 unstaged file: git stash push -- README.md scratch.txt sets it aside instead. Override: trellis allow discard-uncommitted."},
		{"git reset --hard", "trellis: discard-uncommitted: reset --hard discards 1 unstaged file: git stash push sets it aside instead. Override: trellis allow discard-uncommitted."},
		{"git clean -fd", "trellis: discard-uncommitted: clean -fd discards 1 untracked file: git stash push --include-untracked sets it aside instead. Override: trellis allow discard-uncommitted."},
		{"git branch -D unmerged", "trellis: discard-uncommitted: branch -D unmerged discards 1 commit unreachable from any other ref: git branch -m unmerged dropped/unmerged keeps it instead. Override: trellis allow discard-uncommitted."},
		{"git stash drop", "trellis: discard-uncommitted: stash drop discards a stash entry for good. Override: trellis allow discard-uncommitted."},
	} {
		if got := denyLineOf(t, bash(t, w.tree, c.cmd, true)); got != c.want {
			t.Errorf("%q:\n got  %q\n want %q", c.cmd, got, c.want)
		}
	}
	got := denyLineOf(t, bash(t, w.primary, "git worktree remove --force "+filepath.ToSlash(w.tree), true))
	want := "trellis: discard-uncommitted: worktree remove --force discards 1 unstaged file, 1 untracked file in " + w.tree +
		": trellis tree drop tree/x --reason <why> keeps them instead. Override: trellis allow discard-uncommitted."
	if got != want {
		t.Errorf("worktree remove --force:\n got  %q\n want %q", got, want)
	}
}

func TestDiscard_IgnoredFilesNameStashAll(t *testing.T) {
	r := quiet(t)
	tree := testiso.Repo(t)
	testiso.Write(t, filepath.Join(tree, ".gitignore"), "*.log\n")
	testiso.Git(t, tree, "add", ".gitignore")
	testiso.Git(t, tree, "commit", "-qm", "ignore")
	testiso.Write(t, filepath.Join(tree, "a.log"), "x\n")
	testiso.Write(t, filepath.Join(tree, "new.txt"), "n\n")

	d := verdicts(decide(t, bash(t, tree, "git clean -fdx", true), r), kernel.VerdictDeny)
	want := "trellis: discard-uncommitted: clean -fdx discards 1 untracked file, 1 ignored file: git stash push --all sets them aside instead. Override: trellis allow discard-uncommitted."
	if len(d) != 1 || d[0].Line != want {
		t.Errorf("denies %+v, want %q", d, want)
	}
}

func TestPrimaryWrite_TheDenyNamesTreeNew(t *testing.T) {
	w := newWorld(t)
	file := filepath.Join(w.primary, "README.md")

	got := denyLineOf(t, testiso.HookPayload(t, kernel.HookPreToolUse, kernel.ToolEdit, w.primary, map[string]string{"file_path": file}, testiso.WithSession("s")))

	want := "trellis: primary-write: Edit of README.md is in the primary checkout, which is on main: " +
		"work in a tree, trellis tree new <name> makes " + filepath.ToSlash(w.primary) + "--<name>. Override: trellis allow primary-write."
	if got != want {
		t.Errorf("\n got  %q\n want %q", got, want)
	}
}

// Feedback #65: with trees placed inside the primary the deny names where the
// tree will be, so the next call is the right one.
func TestPrimaryWrite_TheDenyNamesTheInsidePathWhenTreesArePlacedInside(t *testing.T) {
	w := newWorld(t)
	testiso.Write(t, filepath.Join(w.primary, "trellis.toml"), "[tree.new]\nplace = \"inside\"\n")
	file := filepath.Join(w.primary, "README.md")

	got := denyLineOf(t, testiso.HookPayload(t, kernel.HookPreToolUse, kernel.ToolEdit, w.primary, map[string]string{"file_path": file}, testiso.WithSession("s")))

	want := "trellis: primary-write: Edit of README.md is in the primary checkout, which is on main: " +
		"work in a tree, trellis tree new <name> makes " + filepath.ToSlash(w.primary) + "/.trellis/trees/<name>. Override: trellis allow primary-write."
	if got != want {
		t.Errorf("\n got  %q\n want %q", got, want)
	}
}

// Feedback #65: an edit in a tree that lives under the primary is an edit of
// the tree, on the tree's branch, never a write to the primary.
func TestPrimaryWrite_ATreeInsideThePrimaryIsNotThePrimary(t *testing.T) {
	w := newWorld(t)
	inside := filepath.Join(w.primary, ".trellis", "trees", "in")
	testiso.Git(t, w.primary, "worktree", "add", "-q", "-b", "in", inside)
	for _, file := range []string{filepath.Join(inside, "README.md"), filepath.Join(inside, "new", "deep.txt")} {
		payload := testiso.HookPayload(t, kernel.HookPreToolUse, kernel.ToolEdit, w.primary, map[string]string{"file_path": file}, testiso.WithSession("s"))
		if d := verdicts(decide(t, payload, quiet(t)), kernel.VerdictDeny); len(d) != 0 {
			t.Errorf("Edit of %s denied: %+v", file, d)
		}
	}
}
