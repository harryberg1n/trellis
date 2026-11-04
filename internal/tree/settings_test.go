package tree

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

// settle commits trellis.toml with text on main and pushes it, so a tree cut
// from origin/main has it.
func (f *fixture) settle(t *testing.T, text string) {
	t.Helper()
	commitFile(t, f.primary, "trellis.toml", text)
	testiso.Git(t, f.primary, "push", "-q", "origin", "main")
}

// Issue #52 item 6: every lane of threadspyp started with
// `ln -s ../threadspyp/node_modules node_modules`; issue #50 item 3: the first
// check of every fanpyp lane paid for setup.
func TestNew_LinksAndRunsWhatTrellisTomlSays(t *testing.T) {
	f := newFixture(t)
	testiso.Write(t, filepath.Join(f.primary, "deps", "lib.txt"), "shared\n")
	testiso.Write(t, filepath.Join(f.primary, ".gitignore"), "/deps/\n")
	testiso.Git(t, f.primary, "add", ".gitignore")
	f.settle(t, "[tree.new]\nlink = [\"deps\"]\nrun = [\"echo warm-up ran\"]\n")

	if err := New(f.env(f.primary), "feat", ""); err != nil {
		t.Fatal(err)
	}

	path := f.primary + "--feat"
	raw, err := os.ReadFile(filepath.Join(path, "deps", "lib.txt"))
	if err != nil || string(raw) != "shared\n" {
		t.Errorf("deps/lib.txt in the tree = %q, %v; want the primary's file through the link", raw, err)
	}
	if got := testiso.Git(t, path, "status", "--porcelain", "--untracked-files=all"); got != "" {
		t.Errorf("status in the tree = %q, want clean: the link is excluded", got)
	}
	out := f.out.String()
	for _, line := range []string{"linked deps\n", "run: echo warm-up ran\n", "warm-up ran"} {
		if !strings.Contains(out, line) {
			t.Errorf("output = %q, want %q", out, line)
		}
	}
	if !strings.HasSuffix(out, path+"\n") {
		t.Errorf("output = %q, want the path last", out)
	}
}

func TestNew_ARunThatFailsSaysSoAndKeepsTheTree(t *testing.T) {
	f := newFixture(t)
	f.settle(t, "[tree.new]\nrun = [\"exit 3\"]\n")

	err := New(f.env(f.primary), "feat", "")

	path := f.primary + "--feat"
	if err == nil || !strings.HasPrefix(err.Error(), `tree new: run "exit 3" failed: `) || !strings.HasSuffix(err.Error(), "; the tree is at "+path) {
		t.Errorf("err = %v, want the failed run and the path", err)
	}
	if !exists(path) {
		t.Errorf("the tree was removed")
	}
}

func TestNew_ALinkWhoseTargetIsMissingIsAWarning(t *testing.T) {
	f := newFixture(t)
	f.settle(t, "[tree.new]\nlink = [\"node_modules\"]\n")

	if err := New(f.env(f.primary), "feat", ""); err != nil {
		t.Fatal(err)
	}

	if want := "trellis: node_modules is not in the primary checkout: not linked\n"; f.errb.String() != want {
		t.Errorf("warnings = %q, want %q", f.errb.String(), want)
	}
}

func TestNew_ABadTrellisTomlStopsWithItsLine(t *testing.T) {
	f := newFixture(t)
	f.settle(t, "[tree.new]\nlnk = [\"x\"]\n")

	err := New(f.env(f.primary), "feat", "")

	if want := "tree new: trellis.toml:2: unknown key tree.new.lnk"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// Issue #53: fanpyp's gate is the local --check; its workflows never run on a
// pull request.
func TestMerge_ChecksNoneDoesNotWait(t *testing.T) {
	f := newFixture(t)
	f.settle(t, "[tree.merge]\nchecks = \"none\"\n")
	path := f.openTree(t)
	f.noChecks = true

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if f.clk.afters != 0 || slices.Contains(f.ghCalls(), "pr checks") {
		t.Errorf("waits %d, gh calls %v; want no wait and no checks read", f.clk.afters, f.ghCalls())
	}
	if !strings.Contains(f.out.String(), `trellis.toml says checks = "none": not waiting for checks`+"\n") {
		t.Errorf("output = %q, want the line", f.out.String())
	}
}

// Issue #52 item 3: during an Actions outage "no checks on this PR" meant the
// runs had not started, and the merge went on.
func TestMerge_RequiredChecksAreWaitedFor(t *testing.T) {
	f := newFixture(t)
	f.settle(t, "[tree.merge]\nrequire = [\"ci\", \"lint\"]\n")
	path := f.openTree(t)
	f.checkSeq = []string{
		"[]",
		`[{"name":"ci","bucket":"pending","link":"L"}]`,
		`[{"name":"ci","bucket":"pass","link":"L"},{"name":"lint","bucket":"pass","link":"L"}]`,
	}

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if f.clk.afters != 2 {
		t.Errorf("waits = %d, want 2 (until lint appeared)", f.clk.afters)
	}
	if !strings.Contains(f.out.String(), "waiting for the required checks: lint\n") {
		t.Errorf("output = %q, want the waiting line naming lint", f.out.String())
	}
}

func TestMerge_ARequiredCheckThatNeverAppearsStops(t *testing.T) {
	f := newFixture(t)
	f.settle(t, "[tree.merge]\nrequire = [\"ci\", \"lint\"]\n")
	path := f.openTree(t)
	f.checkList = `[{"name":"ci","bucket":"pass","link":"L"}]`

	err := Merge(f.env(path), "")

	if want := "tree merge: required checks did not appear in 1800s: lint: run again"; fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
	if slices.Contains(f.ghCalls(), "pr merge") {
		t.Errorf("merged without the required check")
	}
}

// Issue #50 item 5: a checkout whose GitHub remote is called github.
func TestTree_ARemoteNamedInTrellisToml(t *testing.T) {
	f := newFixture(t)
	f.settle(t, "[tree]\nremote = \"github\"\n")
	testiso.Git(t, f.primary, "remote", "rename", "origin", "github")
	testiso.Git(t, f.primary, "remote", "set-head", "github", "main")
	f.mergeSHA = "0123456789abcdef0123456789abcdef01234567"

	if err := New(f.env(f.primary), "feat", ""); err != nil {
		t.Fatal(err)
	}
	path := f.primary + "--feat"
	commitFile(t, path, "mine.txt", "mine\n")
	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(f.out.String(), "deleted github/feat\n") {
		t.Errorf("output = %q, want the branch deleted on github", f.out.String())
	}
	if got := testiso.Git(t, f.origin, "branch", "--list", "feat"); got != "" {
		t.Errorf("the remote still has feat (%q)", got)
	}
}
