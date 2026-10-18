package tree

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
	"github.com/harryberg1n/trellis/internal/testsupport"
)

func TestMain(m *testing.M) { testiso.Main(m) }

// fixture is a primary checkout on main with a bare origin that holds main,
// and a fake gh over the real git.
type fixture struct {
	primary string
	origin  string
	events  []kernel.Event
	out     bytes.Buffer
	gh      *testsupport.FakeRunner
	prSeen  bool   // a pull request exists for the branch
	checks  string // "" for green, else the text of a failed watch
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{primary: testiso.Repo(t)}
	f.origin = filepath.Join(filepath.Dir(f.primary), "origin.git")
	testiso.Git(t, filepath.Dir(f.primary), "init", "--bare", "-q", f.origin)
	testiso.Git(t, f.primary, "remote", "add", "origin", f.origin)
	testiso.Git(t, f.primary, "push", "-q", "-u", "origin", "main")
	f.gh = &testsupport.FakeRunner{Name: "gh", Inner: run.Exec{Clock: clock.Real()}, Answer: f.answer}
	return f
}

func (f *fixture) answer(spec run.Spec) (run.Result, error) {
	switch strings.Join(spec.Args[:2], " ") {
	case "pr view":
		if !f.prSeen {
			return run.Result{ExitCode: 1}, errors.New("gh: no pull requests found for branch")
		}
		return run.Result{Stdout: []byte(`{"number":7,"state":"OPEN"}`)}, nil
	case "pr create":
		f.prSeen = true
		return run.Result{Stdout: []byte("https://github.com/o/r/pull/7\n")}, nil
	case "pr checks":
		if f.checks != "" {
			return run.Result{Stdout: []byte(f.checks), ExitCode: 1}, errors.New("gh: exit status 1")
		}
	}
	return run.Result{}, nil
}

func (f *fixture) env(dir string) Env {
	return Env{
		Dir:    dir,
		Runner: f.gh,
		Out:    &f.out,
		Record: func(ev kernel.Event) error { f.events = append(f.events, ev); return nil },
	}
}

func (f *fixture) kinds() []string {
	ks := make([]string, 0, len(f.events))
	for _, ev := range f.events {
		ks = append(ks, ev.Kind)
	}
	return ks
}

func (f *fixture) ghCalls() []string {
	calls := make([]string, 0, len(f.gh.Specs()))
	for _, s := range f.gh.Specs() {
		calls = append(calls, strings.Join(s.Args[:2], " "))
	}
	return calls
}

// commitFile commits a file in dir with the given content.
func commitFile(t *testing.T, dir, name, content string) {
	t.Helper()
	testiso.Write(t, filepath.Join(dir, name), content)
	testiso.Git(t, dir, "add", name)
	testiso.Git(t, dir, "commit", "-qm", "edit "+name)
}

// openTree makes a tree called feat and returns its path.
func (f *fixture) openTree(t *testing.T) string {
	t.Helper()
	if err := New(f.env(f.primary), "feat", ""); err != nil {
		t.Fatal(err)
	}
	f.out.Reset()
	f.events = nil
	return Path(f.primary, "feat")
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestNew_CreatesWorktreeFromOriginDefaultAndRecordsOpened(t *testing.T) {
	f := newFixture(t)
	commitFile(t, f.primary, "local.txt", "not pushed\n") // local main is ahead of origin/main

	if err := New(f.env(f.primary), "feat/a", ""); err != nil {
		t.Fatal(err)
	}

	path := f.primary + "--feat--a"
	if got, want := f.out.String(), path+"\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	if got, want := testiso.Git(t, path, "rev-parse", "HEAD"), testiso.Git(t, f.primary, "rev-parse", "origin/main"); got != want {
		t.Errorf("tree HEAD = %s, want origin/main %s", got, want)
	}
	if got := testiso.Git(t, path, "branch", "--show-current"); got != "feat/a" {
		t.Errorf("tree branch = %q, want feat/a", got)
	}
	if exists(filepath.Join(path, "local.txt")) {
		t.Errorf("local.txt is in the tree: it branched from local main, not origin/main")
	}
	want := []kernel.Event{openedEvent("feat/a")}
	if !reflect.DeepEqual(f.events, want) {
		t.Errorf("events = %+v, want one tree opened for feat/a", f.events)
	}
	if want := "lane.opened"; f.events[0].Kind != want {
		t.Errorf("kind = %q, want %q", f.events[0].Kind, want)
	}
}

func TestNew_HonoursPath(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(t.TempDir(), "elsewhere")
	if err := New(f.env(f.primary), "x", path); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(path, "README.md")) {
		t.Errorf("no checkout at %s", path)
	}
	if got, want := f.out.String(), path+"\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNew_RefusesExistingBranchOrPath(t *testing.T) {
	f := newFixture(t)
	testiso.Git(t, f.primary, "branch", "taken")
	err := New(f.env(f.primary), "taken", "")
	if err == nil || !strings.Contains(err.Error(), `branch "taken" exists`) {
		t.Errorf("existing branch: err %v, want branch exists", err)
	}

	path := filepath.Join(t.TempDir(), "occupied")
	testiso.Write(t, filepath.Join(path, "f"), "x")
	err = New(f.env(f.primary), "fresh", path)
	if err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("existing path: err %v, want path exists", err)
	}
	if got := testiso.Git(t, f.primary, "branch", "--list", "fresh"); got != "" {
		t.Errorf("branch fresh was created (%q) though the path was refused", got)
	}
	if len(f.events) != 0 {
		t.Errorf("events = %+v, want none from refused trees", f.events)
	}
}

func TestSync_RebasesOntoOrigin(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	commitFile(t, f.primary, "theirs.txt", "theirs\n")
	testiso.Git(t, f.primary, "push", "-q", "origin", "main")

	if err := Sync(f.env(path)); err != nil {
		t.Fatal(err)
	}

	if got, want := testiso.Git(t, path, "rev-parse", "HEAD~1"), testiso.Git(t, f.primary, "rev-parse", "origin/main"); got != want {
		t.Errorf("HEAD~1 = %s, want origin/main %s", got, want)
	}
	if !exists(filepath.Join(path, "theirs.txt")) || !exists(filepath.Join(path, "mine.txt")) {
		t.Errorf("tree lacks theirs.txt or mine.txt after the rebase")
	}
}

func TestSync_StopsOnConflictWithoutReset(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "README.md", "mine\n")
	commitFile(t, f.primary, "README.md", "theirs\n")
	testiso.Git(t, f.primary, "push", "-q", "origin", "main")

	err := Sync(f.env(path))

	if err == nil {
		t.Fatal("sync of a conflicting tree succeeded")
	}
	if got := testiso.Git(t, path, "status"); !strings.Contains(got, "rebase in progress") {
		t.Errorf("status = %q, want the rebase left in progress, not reset", got)
	}
}

func TestMerge_RefusesOnRedCheck(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)

	err := Merge(f.env(path), "exit 1")

	if err == nil || !strings.Contains(err.Error(), "check") {
		t.Fatalf("err = %v, want the red check to stop the merge", err)
	}
	want := []kernel.Event{checkEvent("feat", "red")}
	if !reflect.DeepEqual(f.events, want) {
		t.Errorf("events = %+v, want one ci.result red from source local", f.events)
	}
	if f.events[0].Detail["result"] != "red" || f.events[0].Detail["source"] != "local" {
		t.Errorf("detail = %v, want result red, source local", f.events[0].Detail)
	}
	if calls := f.ghCalls(); len(calls) != 0 {
		t.Errorf("gh calls = %v, want none after a red check", calls)
	}
	if got := testiso.Git(t, f.origin, "branch", "--list", "feat"); got != "" {
		t.Errorf("origin has feat (%q): a red check pushed", got)
	}
}

func TestMerge_RecordsMergedWithPRAndRemovesTree(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")

	if err := Merge(f.env(path), "exit 0"); err != nil {
		t.Fatal(err)
	}

	if got, want := f.kinds(), []string{"ci.result", "lane.merged"}; !reflect.DeepEqual(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
	if f.events[0].Detail["result"] != "green" {
		t.Errorf("check detail = %v, want result green", f.events[0].Detail)
	}
	if got := f.events[1]; got.Lane != "feat" || got.Detail["pr"] != "7" {
		t.Errorf("merged event = %+v, want tree feat with pr 7", got)
	}
	wantCalls := []string{"pr view", "pr create", "pr view", "pr checks", "pr merge"}
	if got := f.ghCalls(); !reflect.DeepEqual(got, wantCalls) {
		t.Errorf("gh calls = %v, want %v", got, wantCalls)
	}
	if got := testiso.Git(t, f.origin, "branch", "--list", "feat"); !strings.Contains(got, "feat") {
		t.Errorf("origin branches = %q, want feat pushed", got)
	}
	if exists(path) {
		t.Errorf("tree %s still exists after the merge", path)
	}
	if got := testiso.Git(t, f.primary, "branch", "--list", "feat"); got != "" {
		t.Errorf("branch feat = %q, want it deleted", got)
	}
}

func TestMerge_StopsOnFailedChecksWithGhOutput(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.checks = "lint\tfail"

	err := Merge(f.env(path), "")

	if err == nil || !strings.Contains(err.Error(), "lint\tfail") {
		t.Fatalf("err = %v, want gh's output in the error", err)
	}
	if !exists(path) {
		t.Errorf("tree removed though the checks failed")
	}
	for _, ev := range f.events {
		if ev.Kind == "lane.merged" {
			t.Errorf("recorded merged though the checks failed")
		}
	}
}

func TestMerge_RefusesOnDefaultBranch(t *testing.T) {
	f := newFixture(t)

	err := Merge(f.env(f.primary), "")

	if err == nil || !strings.Contains(err.Error(), "default branch") {
		t.Errorf("err = %v, want a refusal on the default branch", err)
	}
	if len(f.gh.Specs()) != 0 || len(f.events) != 0 {
		t.Errorf("gh calls %v, events %v, want none", f.ghCalls(), f.events)
	}
}

func TestRm_RefusesDirtyWithoutForce(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	testiso.Write(t, filepath.Join(path, "scratch.txt"), "x")

	err := Rm(f.env(path), "", false)

	if err == nil || !strings.Contains(err.Error(), "1 untracked file") {
		t.Fatalf("err = %v, want a refusal naming 1 untracked file", err)
	}
	if !exists(path) {
		t.Errorf("dirty tree removed without --force")
	}
}

func TestRm_ForceRemovesDirtyTree(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	testiso.Write(t, filepath.Join(path, "scratch.txt"), "x")

	if err := Rm(f.env(f.primary), "feat", true); err != nil {
		t.Fatal(err)
	}

	if exists(path) {
		t.Errorf("tree %s still exists", path)
	}
	if got := testiso.Git(t, f.primary, "branch", "--list", "feat"); got != "" {
		t.Errorf("branch feat = %q, want it deleted", got)
	}
}

func TestRm_RemovesTheCurrentCleanTree(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)

	if err := Rm(f.env(path), "", false); err != nil {
		t.Fatal(err)
	}
	if exists(path) {
		t.Errorf("tree %s still exists", path)
	}
}

func TestRm_RefusesPrimaryCheckout(t *testing.T) {
	f := newFixture(t)
	err := Rm(f.env(f.primary), "", true)
	if err == nil || !strings.Contains(err.Error(), "primary checkout") {
		t.Errorf("err = %v, want a refusal on the primary checkout", err)
	}
	if !exists(f.primary) {
		t.Errorf("primary checkout removed")
	}
}
