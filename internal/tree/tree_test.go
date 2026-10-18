package tree

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/host"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
	"github.com/harryberg1n/trellis/internal/testsupport"
)

func TestMain(m *testing.M) { testiso.Main(m) }

// instantClock fires every After at once and counts them: the poll waits
// without sleeping.
type instantClock struct{ afters int }

func (*instantClock) Now() time.Time { return time.Unix(0, 0) }

func (c *instantClock) After(time.Duration) <-chan time.Time {
	c.afters++
	ch := make(chan time.Time, 1)
	ch <- time.Unix(0, 0)
	return ch
}

// fixture is a primary checkout on main with a bare origin that holds main,
// and a fake gh over the real git.
type fixture struct {
	t         *testing.T
	primary   string
	origin    string
	events    []kernel.Event
	out       bytes.Buffer
	errb      bytes.Buffer
	clk       instantClock
	gh        *testsupport.FakeRunner
	prSeen    bool              // a pull request exists for the branch
	mergeable []string          // what successive pr views say; the last repeats; empty is MERGEABLE
	review    string            // the reviewDecision of the PR
	mergeErr  string            // the failure of pr merge; "" merges
	head      string            // the PR head sha; "" is the local HEAD of the tree
	watchFail string            // the text of a failed watch; "" is green
	noChecks  bool              // the PR has no checks
	checkList string            // the JSON `pr checks --json` prints; "" is one passing check
	checkSeq  []string          // successive JSON reads of the checks; the last repeats; used before checkList
	workflows string            // what the workflow count prints; "" is one workflow
	checksErr bool              // pr checks cannot be read
	mergeSHA  string            // the merge commit pr view reports once merged; "" is none
	logs      map[string]string // the failed log of a run id
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, primary: testiso.Repo(t)}
	f.origin = filepath.Join(filepath.Dir(f.primary), "origin.git")
	testiso.Git(t, filepath.Dir(f.primary), "init", "--bare", "-q", f.origin)
	testiso.Git(t, f.primary, "remote", "add", "origin", f.origin)
	testiso.Git(t, f.primary, "push", "-q", "-u", "origin", "main")
	testiso.Git(t, f.primary, "remote", "set-head", "origin", "main")
	f.gh = &testsupport.FakeRunner{Name: "gh", Inner: run.Exec{Clock: clock.Real()}, Answer: f.answer}
	return f
}

func (f *fixture) answer(spec run.Spec) (run.Result, error) {
	switch strings.Join(spec.Args[:2], " ") {
	case "pr view":
		if slices.Contains(spec.Args, "mergeCommit") {
			if f.mergeSHA == "" {
				return run.Result{Stdout: []byte(`{"mergeCommit":null}`)}, nil
			}
			return run.Result{Stdout: []byte(`{"mergeCommit":{"oid":"` + f.mergeSHA + `"}}`)}, nil
		}
	case "pr list":
		if !f.prSeen {
			return run.Result{Stdout: []byte("[]")}, nil
		}
		state := "MERGEABLE"
		if len(f.mergeable) > 0 {
			state = f.mergeable[0]
			if len(f.mergeable) > 1 {
				f.mergeable = f.mergeable[1:]
			}
		}
		head := f.head
		if head == "" {
			head = testiso.Git(f.t, spec.Dir, "rev-parse", "HEAD")
		}
		status := "CLEAN"
		switch state {
		case "CONFLICTING":
			status = "DIRTY"
		case "BLOCKED":
			state, status = "MERGEABLE", "BLOCKED"
		case "UNSTABLE":
			state, status = "MERGEABLE", "UNSTABLE"
		case "BEHIND":
			state, status = "MERGEABLE", "BEHIND"
		}
		body := fmt.Sprintf(`[{"number":7,"state":"OPEN","mergeable":%q,"mergeStateStatus":%q,"headRefOid":%q,"reviewDecision":%q}]`, state, status, head, f.review)
		return run.Result{Stdout: []byte(body)}, nil
	case "pr create":
		f.prSeen = true
		return run.Result{Stdout: []byte("https://github.com/o/r/pull/7\n")}, nil
	case "pr checks":
		if slices.Contains(spec.Args, "--json") {
			if f.checksErr {
				return run.Result{ExitCode: 1}, errors.New("gh: network")
			}
			out := f.checkList
			switch {
			case len(f.checkSeq) > 0:
				out = f.checkSeq[0]
				if len(f.checkSeq) > 1 {
					f.checkSeq = f.checkSeq[1:]
				}
			case f.noChecks:
				return run.Result{ExitCode: 1}, errors.New("gh: no checks reported on the 'feat' branch")
			case out == "":
				out = `[{"name":"ci","bucket":"pass","link":"L"}]`
			}
			return run.Result{Stdout: []byte(out), ExitCode: 1}, errors.New("gh: exit status 1")
		}
		if f.noChecks {
			return run.Result{ExitCode: 1}, errors.New("gh: no checks reported on the 'feat' branch")
		}
		if f.watchFail != "" {
			return run.Result{Stdout: []byte(f.watchFail), ExitCode: 1}, errors.New("gh: exit status 1")
		}
	case "api repos/{owner}/{repo}/actions/workflows":
		if f.workflows == "" {
			return run.Result{Stdout: []byte("1\n")}, nil
		}
		return run.Result{Stdout: []byte(f.workflows)}, nil
	case "pr merge":
		if f.mergeErr != "" {
			return run.Result{ExitCode: 1}, errors.New(f.mergeErr)
		}
	case "run view":
		return run.Result{Stdout: []byte(f.logs[spec.Args[2]])}, nil
	}
	return run.Result{}, nil
}

func (f *fixture) env(dir string) Env {
	return Env{
		Dir:    dir,
		Runner: f.gh,
		Clock:  &f.clk,
		Out:    &f.out,
		Err:    &f.errb,
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
	if want := "tree.opened"; f.events[0].Kind != want {
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

	if got, want := f.kinds(), []string{"ci.result", "tree.merged"}; !reflect.DeepEqual(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
	if f.events[0].Detail["result"] != "green" {
		t.Errorf("check detail = %v, want result green", f.events[0].Detail)
	}
	if got := f.events[1]; got.Tree != "feat" || got.Detail["pr"] != "7" {
		t.Errorf("merged event = %+v, want tree feat with pr 7", got)
	}
	wantCalls := []string{"auth status", "pr list", "pr create", "pr list", "pr checks", "pr checks", "pr list", "pr merge", "pr view"}
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

// fixable is err as the *Fixable an operator can act on, or nil.
func fixable(err error) *Fixable {
	var fx *Fixable
	if errors.As(err, &fx) {
		return fx
	}
	return nil
}

func TestMerge_ExplainsRedCIWithFailingLines(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.watchFail = "test\tfail\t1m\thttps://github.com/o/r/actions/runs/99/job/5"
	f.checkList = `[{"name":"lint","bucket":"pass","link":"https://github.com/o/r/actions/runs/98/job/4"},` +
		`{"name":"test","bucket":"fail","link":"https://github.com/o/r/actions/runs/99/job/5"}]`
	f.logs = map[string]string{"99": "test\tRun tests\t2026-10-18T10:00:00.1234567Z === RUN   TestA\n" +
		"test\tRun tests\t2026-10-18T10:00:01.0000000Z --- FAIL: TestA (0.00s)\n" +
		"test\tRun tests\t2026-10-18T10:00:01.5000000Z     a_test.go:12: got 1, want 2\n" +
		"test\tRun tests\t2026-10-18T10:00:02.0000000Z FAIL\tgithub.com/o/r/pkg\t0.01s\n" +
		"test\tRun tests\t2026-10-18T10:00:03.0000000Z ##[error]Process completed with exit code 1.\n" +
		"test\tRun tests\t2026-10-18T10:00:04.0000000Z cleaning up\n"}

	err := Merge(f.env(path), "")

	fx := fixable(err)
	if fx == nil {
		t.Fatalf("err = %v, want a Fixable red CI", err)
	}
	want := "tree merge: 1 check did not pass\n" +
		"test  https://github.com/o/r/actions/runs/99/job/5\n" +
		"  --- FAIL: TestA (0.00s)\n" +
		"  FAIL\tgithub.com/o/r/pkg\t0.01s\n" +
		"  ##[error]Process completed with exit code 1."
	if err.Error() != want {
		t.Errorf("error =\n%s\nwant\n%s", err.Error(), want)
	}
	if !exists(path) {
		t.Errorf("tree removed though the checks failed")
	}
	if slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("recorded merged though the checks failed")
	}
}

func TestMerge_ConflictNamesFilesAndSaysSync(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "README.md", "mine\n")
	commitFile(t, f.primary, "README.md", "theirs\n")
	testiso.Git(t, f.primary, "push", "-q", "origin", "main")
	f.mergeable = []string{"CONFLICTING"}

	err := Merge(f.env(path), "")

	if fixable(err) == nil {
		t.Fatalf("err = %v, want a Fixable conflict", err)
	}
	want := "tree merge: conflicts with origin/main in: README.md\nrun trellis tree sync, resolve, and merge again"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if slices.Contains(f.ghCalls(), "pr checks") {
		t.Errorf("gh calls = %v, want no wait after a conflict", f.ghCalls())
	}
}

func TestMerge_BehindSaysSync(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeable = []string{"BEHIND"}

	err := Merge(f.env(path), "")

	if fixable(err) == nil || err.Error() != "tree merge: behind origin/main: run trellis tree sync" {
		t.Errorf("err = %v, want fixable behind", err)
	}
}

func TestMerge_NoChecksMergesWhenMergeable(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.noChecks = true

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(f.out.String(), "no checks on this PR\n") {
		t.Errorf("output = %q, want the line no checks on this PR", f.out.String())
	}
	if !slices.Contains(f.kinds(), "tree.merged") || exists(path) {
		t.Errorf("kinds %v, tree exists %v; want merged and removed", f.kinds(), exists(path))
	}
}

func TestMerge_HeadMovedStops(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	f.head = "0123456789abcdef0123456789abcdef01234567"

	err := Merge(f.env(path), "")

	local := testiso.Git(t, path, "rev-parse", "HEAD")[:12]
	want := "tree merge: head moved: local " + local + " vs PR 0123456789ab"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
	if slices.Contains(f.ghCalls(), "pr merge") {
		t.Errorf("merged though the head moved")
	}
}

func TestMerge_PollsUnknownMergeableWithoutSleeping(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeable = []string{"UNKNOWN", "UNKNOWN", "MERGEABLE"}

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if f.clk.afters != 2 {
		t.Errorf("waits on the clock = %d, want 2 (three reads)", f.clk.afters)
	}
}

const pendingCheck = `[{"name":"ci","bucket":"pending","link":"L"}]`

func TestMerge_WaitsForChecksToAppearAfterThePush(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.checkSeq = []string{"[]", "[]", pendingCheck, `[{"name":"ci","bucket":"pass","link":"L"}]`}

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if f.clk.afters != 2 {
		t.Errorf("waits on the clock = %d, want 2 (two empty reads)", f.clk.afters)
	}
	if !strings.Contains(f.out.String(), "checks passed on #7\n") || strings.Contains(f.out.String(), "no checks") {
		t.Errorf("output = %q, want checks passed and no 'no checks'", f.out.String())
	}
	if !slices.Contains(f.kinds(), "tree.merged") || exists(path) {
		t.Errorf("kinds %v, tree exists %v; want merged and removed", f.kinds(), exists(path))
	}
}

func TestMerge_NoChecksAfterTheWindowAndCleanMerges(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.noChecks = true

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if f.clk.afters != 18 {
		t.Errorf("waits on the clock = %d, want 18 (90 s in 5 s steps)", f.clk.afters)
	}
	if !strings.Contains(f.out.String(), "no checks on this PR\n") || !slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("output %q, kinds %v; want the no-checks line and a merge", f.out.String(), f.kinds())
	}
}

func TestMerge_NoChecksAfterTheWindowAndNotCleanStopsWithoutMerging(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.noChecks = true
	f.mergeable = []string{"MERGEABLE", "UNSTABLE"}

	err := Merge(f.env(path), "")

	want := "tree merge: no checks appeared in 90s and the merge state is UNSTABLE: run again"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
	if slices.Contains(f.ghCalls(), "pr merge") {
		t.Errorf("merged without checks on an unclean PR")
	}
}

func TestMerge_RepositoryWithoutWorkflowsMergesAtOnce(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.noChecks = true
	f.workflows = "0\n"

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if f.clk.afters != 0 {
		t.Errorf("waits on the clock = %d, want 0", f.clk.afters)
	}
	if !strings.Contains(f.out.String(), "no checks on this PR\n") || !slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("output %q, kinds %v; want the no-checks line and a merge", f.out.String(), f.kinds())
	}
}

func TestMerge_UnstableWhileChecksPendingKeepsWatching(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeable = []string{"MERGEABLE", "UNSTABLE", "MERGEABLE"}
	f.checkSeq = []string{pendingCheck}

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if f.clk.afters != 1 {
		t.Errorf("waits on the clock = %d, want 1 (one more watch)", f.clk.afters)
	}
	if !slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("kinds %v, want merged", f.kinds())
	}
}

func TestMerge_RecordsTheMergeCommitOnTheRemotePath(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeSHA = "0123456789abcdef0123456789abcdef01234567"

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"pr": "7", "merge": "0123456789abcdef0123456789abcdef01234567"}
	if got := f.events[len(f.events)-1]; got.Kind != "tree.merged" || got.Tree != "feat" || !reflect.DeepEqual(got.Detail, want) {
		t.Errorf("merged event = %+v, want tree feat with detail %v", got, want)
	}
}

func TestFailingLines_KeepsMatchesStripsPrefixesAndCapsAtForty(t *testing.T) {
	var log strings.Builder
	log.WriteString("job\tstep\t2026-10-18T10:00:00.0000000Z noise\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&log, "job\tstep\t2026-10-18T10:00:00.0000000Z --- FAIL: Test%d\n", i)
	}
	log.WriteString("panic: boom\n") // no columns at all
	got := failingLines(log.String())
	if len(got) != 40 || got[0] != "--- FAIL: Test0" || got[39] != "--- FAIL: Test39" {
		t.Errorf("got %d lines, first %q, last %q; want 40, Test0 .. Test39", len(got), got[0], got[len(got)-1])
	}
	if got := failingLines("panic: boom\nError: bad\nerror: worse\nnothing\n"); !reflect.DeepEqual(got, []string{"panic: boom", "Error: bad", "error: worse"}) {
		t.Errorf("bare lines = %q", got)
	}
}

func TestSync_ConflictListsFilesAndNextStep(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "README.md", "mine\n")
	commitFile(t, f.primary, "README.md", "theirs\n")
	testiso.Git(t, f.primary, "push", "-q", "origin", "main")

	err := Sync(f.env(path))

	if fixable(err) == nil {
		t.Fatalf("err = %v, want a Fixable conflict (exit 2)", err)
	}
	msg := err.Error()
	for _, want := range []string{"CONFLICT (content): Merge conflict in README.md", "conflicted: README.md"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
	if last := msg[strings.LastIndex(msg, "\n")+1:]; last != "resolve, git add, git rebase --continue; or git rebase --abort" {
		t.Errorf("last line = %q, want the next step", last)
	}
}

// newLocalFixture is a repo with no remote at all, and a gh that must not be
// asked anything.
func newLocalFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, primary: testiso.Repo(t)}
	f.gh = &testsupport.FakeRunner{Name: "gh", Inner: run.Exec{Clock: clock.Real()}, Answer: func(run.Spec) (run.Result, error) {
		return run.Result{}, errors.New("gh must not run in a local repo")
	}}
	return f
}

func TestNew_LocalRepoBranchesFromTheLocalDefault(t *testing.T) {
	f := newLocalFixture(t)
	commitFile(t, f.primary, "local.txt", "x\n")

	if err := New(f.env(f.primary), "feat", ""); err != nil {
		t.Fatal(err)
	}

	if !exists(filepath.Join(f.primary+"--feat", "local.txt")) {
		t.Errorf("the tree lacks local.txt: it was not cut from the local main")
	}
}

func TestMerge_LocalRebaseIsTheNoFFMerge(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")

	if err := Merge(f.env(path), "exit 0", Rebase()); err != nil {
		t.Fatal(err)
	}

	if got := testiso.Git(t, f.primary, "log", "-1", "--format=%s"); got != "Merge tree feat" {
		t.Errorf("subject = %q, want Merge tree feat", got)
	}
	if got := testiso.Git(t, f.primary, "log", "-1", "--format=%b"); got != "- edit mine.txt" {
		t.Errorf("body = %q, want the branch's subject lines", got)
	}
	if got := len(strings.Fields(testiso.Git(t, f.primary, "rev-list", "--parents", "-n1", "HEAD"))); got != 3 {
		t.Errorf("HEAD has %d fields in rev-list --parents, want 3 (a merge commit)", got)
	}
	if got, want := f.kinds(), []string{"ci.result", "tree.merged"}; !reflect.DeepEqual(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
	detail := f.events[1].Detail
	if _, has := detail["pr"]; has || detail["merge"] != testiso.Git(t, f.primary, "rev-parse", "HEAD") {
		t.Errorf("merged detail = %v, want merge = the commit and no pr", detail)
	}
	if exists(path) {
		t.Errorf("tree %s still exists", path)
	}
	if got := testiso.Git(t, f.primary, "branch", "--list", "feat"); got != "" {
		t.Errorf("branch feat = %q, want it deleted", got)
	}
	if len(f.gh.Specs()) != 0 {
		t.Errorf("gh calls = %v, want none", f.ghCalls())
	}
}

func TestMerge_LocalRebaseConflictAbortsNamesFilesExit2(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "README.md", "mine\n")
	commitFile(t, f.primary, "README.md", "theirs\n")

	err := Merge(f.env(path), "", Rebase())

	want := "tree merge: conflicts with main in: README.md\nrun trellis tree sync in the tree, resolve, merge again"
	if fixable(err) == nil || err.Error() != want {
		t.Fatalf("err = %v, want fixable %q", err, want)
	}
	if got := testiso.Git(t, f.primary, "status", "--porcelain"); got != "" {
		t.Errorf("primary status = %q, want the merge aborted", got)
	}
	if !exists(path) || slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("tree exists %v, kinds %v; want the tree kept and no merge recorded", exists(path), f.kinds())
	}
}

func TestMerge_LocalRefusesDirtyPrimary(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	testiso.Write(t, filepath.Join(f.primary, "README.md"), "edited in the primary\n")

	err := Merge(f.env(path), "")

	if err == nil || !strings.Contains(err.Error(), "primary checkout") || !strings.Contains(err.Error(), "1 unstaged file") {
		t.Errorf("err = %v, want a refusal naming the dirty primary checkout", err)
	}
	if !exists(path) {
		t.Errorf("tree removed")
	}
}

// The primary checkout of every installed repository holds an untracked
// .claude/settings.local.json that trellis itself wrote; untracked files
// never block a landing, since git refuses on its own when a merge would
// overwrite one.
func TestMerge_LocalIgnoresUntrackedFilesInThePrimary(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	settings := filepath.Join(f.primary, ".claude", "settings.local.json")
	testiso.Write(t, settings, "{}\n")
	testiso.Write(t, filepath.Join(f.primary, "scratch.txt"), "x")

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if got := testiso.Git(t, f.primary, "log", "-1", "--format=%s"); got != "Merge tree feat" {
		t.Errorf("subject = %q, want Merge tree feat", got)
	}
	if !exists(settings) || !exists(filepath.Join(f.primary, "scratch.txt")) {
		t.Errorf("an untracked file of the primary is gone after the merge")
	}
}

func TestMerge_LocalRefusesPrimaryOffTheDefaultBranch(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	testiso.Git(t, f.primary, "checkout", "-q", "-b", "other")

	err := Merge(f.env(path), "")

	if err == nil || !strings.Contains(err.Error(), "primary checkout is on other, not main") {
		t.Errorf("err = %v, want a refusal naming the branch", err)
	}
}

func TestMerge_RemoteWithoutGhStopsInOneLine(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.gh.Answer = func(spec run.Spec) (run.Result, error) {
		if spec.Args[0] == "auth" {
			return run.Result{ExitCode: 1}, errors.New("gh: not logged in")
		}
		return run.Result{}, nil
	}

	err := Merge(f.env(path), "")

	if err == nil || fixable(err) != nil || err.Error() != "tree merge: gh is not installed or not authenticated" {
		t.Errorf("err = %v, want the one-line gh refusal", err)
	}
	if got := testiso.Git(t, f.primary, "branch", "--list", "-r", "origin/feat"); got != "" {
		t.Errorf("pushed (%q) though gh was not usable", got)
	}
}

func TestRm_FindsTreeMadeWithCustomPath(t *testing.T) {
	f := newFixture(t)
	custom := filepath.Join(t.TempDir(), "my", "wt")
	testiso.Git(t, f.primary, "worktree", "add", "-q", "-b", "hand/made", custom)

	if err := Rm(f.env(f.primary), "hand/made", false); err != nil {
		t.Fatal(err)
	}

	if exists(custom) {
		t.Errorf("tree %s still exists", custom)
	}
	if got := testiso.Git(t, f.primary, "branch", "--list", "hand/made"); got != "" {
		t.Errorf("branch = %q, want it deleted", got)
	}
}

func TestMerge_RefusesDirtyTree(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	testiso.Write(t, filepath.Join(path, "scratch.txt"), "x")

	err := Merge(f.env(path), "")

	if err == nil || fixable(err) != nil || err.Error() != "tree merge: feat has 1 untracked file; commit or discard it first" {
		t.Errorf("err = %v, want the one-line dirty refusal (exit 1)", err)
	}
	if len(f.gh.Specs()) != 0 {
		t.Errorf("gh calls = %v, want none", f.ghCalls())
	}
	if got := testiso.Git(t, f.primary, "branch", "--list", "-r", "origin/feat"); got != "" {
		t.Errorf("pushed (%q) from a dirty tree", got)
	}
}

func TestMerge_RefusesTreeTheCheckDirtied(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)

	err := Merge(f.env(path), "echo x>out.txt")

	want := "tree merge: the check left 1 untracked file in feat; commit, ignore or clean them first"
	if err == nil || fixable(err) != nil || err.Error() != want {
		t.Errorf("err = %v, want %q (exit 1)", err, want)
	}
	if len(f.gh.Specs()) != 0 {
		t.Errorf("gh calls = %v, want none", f.ghCalls())
	}
	if got := testiso.Git(t, f.primary, "branch", "--list", "-r", "origin/feat"); got != "" {
		t.Errorf("pushed (%q) after the check dirtied the tree", got)
	}
}

func TestMerge_PushFailureIsOneLine(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	env := f.env(path)
	env.Runner = failing{
		Inner: f.gh, Verb: []string{"push"},
		Err: "git: exit status 1: error: failed to push some refs\nhint: updates were rejected",
	}

	err := Merge(env, "")

	if want := "tree merge: push failed: error: failed to push some refs"; err == nil || fixable(err) != nil || err.Error() != want {
		t.Errorf("err = %v, want %q (exit 1)", err, want)
	}
	if slices.Contains(f.ghCalls(), "pr create") {
		t.Errorf("opened a PR after a failed push")
	}
}

func TestMerge_MergeFailureIsOneLine(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeErr = "gh: exit status 1: GraphQL: Pull request is not mergeable\nsecond line"

	err := Merge(f.env(path), "")

	if want := "tree merge: merge failed: GraphQL: Pull request is not mergeable"; err == nil || fixable(err) != nil || err.Error() != want {
		t.Errorf("err = %v, want %q (exit 1)", err, want)
	}
	if !exists(path) {
		t.Errorf("tree removed though the merge failed")
	}
}

func TestMerge_LocalRefusesDirtyTree(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	testiso.Write(t, filepath.Join(path, "scratch.txt"), "x")

	err := Merge(f.env(path), "")

	if err == nil || err.Error() != "tree merge: feat has 1 untracked file; commit or discard it first" {
		t.Errorf("err = %v, want the one-line dirty refusal", err)
	}
	if !exists(path) {
		t.Errorf("tree removed")
	}
}

// failing is a Runner that fails the git children whose arguments start with
// the given words and runs the rest on Inner.
type failing struct {
	Inner run.Runner
	Verb  []string
	Err   string // the failure text; default "git: forced failure"
}

func (f failing) Light(spec run.Spec) (run.Result, error) {
	if spec.Name == "git" && len(spec.Args) >= len(f.Verb) && slices.Equal(spec.Args[:len(f.Verb)], f.Verb) {
		return run.Result{ExitCode: 128}, errors.New(cmp.Or(f.Err, "git: forced failure"))
	}
	return f.Inner.Light(spec)
}

func TestMerge_LocalConflictWithUnreadableFileListStillAborts(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "README.md", "mine\n")
	commitFile(t, f.primary, "README.md", "theirs\n")
	env := f.env(path)
	env.Runner = failing{Inner: f.gh, Verb: []string{"diff", "--name-only"}}

	err := Merge(env, "", Rebase())

	want := "tree merge: conflicts with main (files unknown)\nrun trellis tree sync in the tree, resolve, merge again"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
	if exists(filepath.Join(f.primary, ".git", "MERGE_HEAD")) {
		t.Errorf("MERGE_HEAD exists: the primary was left merging")
	}
}

func TestMerge_LocalAbortFailureIsItsOwnLine(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "README.md", "mine\n")
	commitFile(t, f.primary, "README.md", "theirs\n")
	env := f.env(path)
	env.Runner = failing{Inner: f.gh, Verb: []string{"merge", "--abort"}}

	err := Merge(env, "", Rebase())

	if err == nil || fixable(err) != nil || !strings.HasPrefix(err.Error(), "tree merge: could not abort the merge in "+f.primary+": ") {
		t.Errorf("err = %v, want the abort failure on its own", err)
	}
}

func TestMerge_StillUnknownMergeableAsksToRunAgain(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeable = []string{"UNKNOWN"}

	err := Merge(f.env(path), "")

	want := "tree merge: mergeability not computed yet by GitHub: run again"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
	if slices.Contains(f.ghCalls(), "pr merge") || slices.Contains(f.ghCalls(), "pr checks") {
		t.Errorf("gh calls = %v, want no wait or merge", f.ghCalls())
	}
}

func TestMerge_CancelledChecksAreListed(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.watchFail = "x"
	f.checkList = `[{"name":"lint","bucket":"cancel","link":"https://github.com/o/r/actions/runs/98/job/4"}]`

	err := Merge(f.env(path), "")

	want := "tree merge: 1 check did not pass\ncancelled: lint"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
}

func TestMerge_UnreadableChecksSayWhyWithExitOne(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.watchFail = "x"
	f.checkList = `[{"name":"lint","bucket":"pass","link":"L"}]`

	err := Merge(f.env(path), "")

	if err == nil || fixable(err) != nil || !strings.HasPrefix(err.Error(), "tree merge: checks could not be read: ") || strings.Contains(err.Error(), "\n") {
		t.Errorf("err = %q, want one line starting with checks could not be read (exit 1)", err)
	}
	if slices.Contains(f.ghCalls(), "pr merge") {
		t.Errorf("merged though the checks could not be read")
	}
}

func TestMerge_BlockedByBranchProtectionStopsBeforeMerging(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeable = []string{"MERGEABLE", "BLOCKED"}
	f.review = "REVIEW_REQUIRED"

	err := Merge(f.env(path), "")

	want := "tree merge: blocked by branch protection: review required"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
	if slices.Contains(f.ghCalls(), "pr merge") {
		t.Errorf("merged a blocked PR")
	}
}

func TestMerge_UnstableAfterAGreenWatchIsRed(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeable = []string{"MERGEABLE", "UNSTABLE"}
	f.checkList = `[{"name":"lint","bucket":"cancel","link":"L"}]`

	err := Merge(f.env(path), "")

	want := "tree merge: 1 check did not pass\ncancelled: lint"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
}

func TestFailingLines_StripsAByteOrderMark(t *testing.T) {
	got := failingLines("\ufeff2026-10-18T10:00:00.0000000Z --- FAIL: TestA\n")
	if !reflect.DeepEqual(got, []string{"--- FAIL: TestA"}) {
		t.Errorf("got %q, want the line without the BOM, job, step and timestamp", got)
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

func TestMerge_SquashesByDefaultAndRecords(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeSHA = "0123456789abcdef0123456789abcdef01234567"

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if got, want := f.mergeArgs(), []string{"pr", "merge", "7", "--squash", "--delete-branch"}; !reflect.DeepEqual(got, want) {
		t.Errorf("gh merge argv = %v, want %v", got, want)
	}
	want := map[string]string{"pr": "7", "merge": "0123456789abcdef0123456789abcdef01234567"}
	if got := f.events[len(f.events)-1]; got.Kind != "tree.merged" || !reflect.DeepEqual(got.Detail, want) {
		t.Errorf("merged event = %+v, want detail %v", got, want)
	}
}

func TestMerge_RebasePassesRebaseToGh(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)

	if err := Merge(f.env(path), "", Rebase()); err != nil {
		t.Fatal(err)
	}

	if got, want := f.mergeArgs(), []string{"pr", "merge", "7", "--rebase", "--delete-branch"}; !reflect.DeepEqual(got, want) {
		t.Errorf("gh merge argv = %v, want %v", got, want)
	}
}

func TestMerge_LocalSquashMakesOneCommitWithTheTreeSubject(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "one.txt", "1\n")
	commitFile(t, path, "two.txt", "2\n")

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if got := testiso.Git(t, f.primary, "log", "-1", "--format=%s"); got != "Merge tree feat" {
		t.Errorf("subject = %q, want Merge tree feat", got)
	}
	if got := testiso.Git(t, f.primary, "log", "-1", "--format=%b"); got != "- edit one.txt\n- edit two.txt" {
		t.Errorf("body = %q, want the branch's subject lines", got)
	}
	if got := len(strings.Fields(testiso.Git(t, f.primary, "rev-list", "--parents", "-n1", "HEAD"))); got != 2 {
		t.Errorf("HEAD has %d fields in rev-list --parents, want 2 (one parent, not a merge)", got)
	}
	if got := testiso.Git(t, f.primary, "ls-files", "one.txt", "two.txt"); got != "one.txt\ntwo.txt" {
		t.Errorf("tracked = %q, want both files", got)
	}
	if detail := f.events[len(f.events)-1].Detail; detail["merge"] != testiso.Git(t, f.primary, "rev-parse", "HEAD") {
		t.Errorf("merged detail = %v, want merge = the commit", detail)
	}
	if exists(path) {
		t.Errorf("tree %s still exists", path)
	}
}

func TestMerge_LocalSquashConflictLeavesThePrimaryClean(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "README.md", "mine\n")
	commitFile(t, path, "added.txt", "new\n")
	commitFile(t, f.primary, "README.md", "theirs\n")
	before := testiso.Git(t, f.primary, "rev-parse", "HEAD")

	err := Merge(f.env(path), "")

	want := "tree merge: conflicts with main in: README.md\nrun trellis tree sync in the tree, resolve, merge again"
	if fixable(err) == nil || err.Error() != want {
		t.Fatalf("err = %v, want fixable %q", err, want)
	}
	if got := testiso.Git(t, f.primary, "status", "--porcelain", "--untracked-files=no"); got != "" {
		t.Errorf("primary status = %q, want clean", got)
	}
	if exists(filepath.Join(f.primary, "added.txt")) {
		t.Errorf("added.txt left in the primary")
	}
	if got := testiso.Git(t, f.primary, "rev-parse", "HEAD"); got != before {
		t.Errorf("HEAD moved to %s, want %s", got, before)
	}
	if !exists(path) || slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("tree exists %v, kinds %v; want the tree kept and no merge recorded", exists(path), f.kinds())
	}
	for _, name := range []string{"SQUASH_MSG", "MERGE_MSG", "AUTO_MERGE"} {
		if p := testiso.Git(t, f.primary, "rev-parse", "--git-path", name); exists(filepath.Join(f.primary, p)) {
			t.Errorf("%s left in the primary's git dir", name)
		}
	}
}

func TestMerge_LocalSquashUndoTreatsPathsLiterally(t *testing.T) {
	f := newLocalFixture(t)
	commitFile(t, f.primary, "i.tsx", "clean\n")
	commitFile(t, f.primary, "README.md", "base\n")
	path := f.openTree(t)
	commitFile(t, path, "[id].tsx", "glob\n")
	commitFile(t, path, "README.md", "mine\n")
	commitFile(t, f.primary, "README.md", "theirs\n")

	err := Merge(f.env(path), "")

	if fixable(err) == nil {
		t.Fatalf("err = %v, want a fixable conflict", err)
	}
	if got := testiso.Git(t, f.primary, "ls-files", "i.tsx"); got != "i.tsx" {
		t.Errorf("tracked = %q, want i.tsx still tracked", got)
	}
	if b, rerr := os.ReadFile(filepath.Join(f.primary, "i.tsx")); rerr != nil || string(b) != "clean\n" {
		t.Errorf("i.tsx = %q, %v; want unchanged", b, rerr)
	}
	if exists(filepath.Join(f.primary, "[id].tsx")) {
		t.Errorf("[id].tsx left in the primary")
	}
	if got := testiso.Git(t, f.primary, "status", "--porcelain", "--untracked-files=no"); got != "" {
		t.Errorf("primary status = %q, want clean", got)
	}
}

func TestMerge_LocalSquashUndoHandlesNonASCIINames(t *testing.T) {
	f := newLocalFixture(t)
	commitFile(t, f.primary, "café.txt", "base\n")
	path := f.openTree(t)
	commitFile(t, path, "café.txt", "mine\n")
	commitFile(t, f.primary, "café.txt", "theirs\n")

	err := Merge(f.env(path), "")

	if fixable(err) == nil {
		t.Fatalf("err = %v, want a fixable conflict", err)
	}
	if got := testiso.Git(t, f.primary, "status", "--porcelain", "--untracked-files=no"); got != "" {
		t.Errorf("primary status = %q, want clean", got)
	}
	if b, rerr := os.ReadFile(filepath.Join(f.primary, "café.txt")); rerr != nil || string(b) != "theirs\n" {
		t.Errorf("café.txt = %q, %v; want the primary's content", b, rerr)
	}
}

func TestMerge_WarnsWhenTheMergeCommitIsNotReportedYet(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if got, want := f.errb.String(), "trellis: merge commit not reported yet; the report links this tree by subject\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if got := f.events[len(f.events)-1]; got.Kind != "tree.merged" || !reflect.DeepEqual(got.Detail, map[string]string{"pr": "7"}) {
		t.Errorf("merged event = %+v, want detail pr only", got)
	}
}

func TestMerge_ReadFailureOfChecksKeepsWatching(t *testing.T) {
	f := newFixture(t)
	f.checksErr = true

	if !anyPending(host.GH{Runner: f.gh, Dir: f.primary}, 7) {
		t.Errorf("anyPending = false on a read failure, want true")
	}
}

// mergeArgs is the argv of the last gh pr merge.
func (f *fixture) mergeArgs() []string {
	var args []string
	for _, s := range f.gh.Specs() {
		if strings.Join(s.Args[:2], " ") == "pr merge" {
			args = s.Args
		}
	}
	return args
}

func TestRm_RefusesUnmergedCommitsWithoutForce(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "a.txt", "1\n")
	commitFile(t, path, "b.txt", "2\n")
	commitFile(t, path, "c.txt", "3\n")

	err := Rm(f.env(path), "", false)

	if err == nil || !strings.Contains(err.Error(), "3 commits not merged into origin/main") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a refusal naming 3 unmerged commits and --force", err)
	}
	if !exists(path) {
		t.Errorf("tree with unmerged commits removed without --force")
	}
	if got := testiso.Git(t, f.primary, "branch", "--list", "feat"); got == "" {
		t.Errorf("branch feat deleted without --force")
	}
}

func TestRm_RemovesABranchMergedIntoOrigin(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "a.txt", "1\n")
	testiso.Git(t, path, "push", "-q", "origin", "feat:main")
	testiso.Git(t, f.primary, "fetch", "-q", "origin")

	if err := Rm(f.env(path), "", false); err != nil {
		t.Fatal(err)
	}
	if exists(path) {
		t.Errorf("merged tree %s still exists", path)
	}
}

func TestRm_RemovesABranchItsUpstreamHolds(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "a.txt", "1\n")
	testiso.Git(t, path, "push", "-q", "-u", "origin", "feat")

	if err := Rm(f.env(path), "", false); err != nil {
		t.Fatal(err)
	}
	if exists(path) {
		t.Errorf("pushed tree %s still exists", path)
	}
}

func TestRm_ForceRemovesUnmergedCommits(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "a.txt", "1\n")

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

func TestRm_LocalRepoRefusesCommitsNotOnTheLocalDefault(t *testing.T) {
	f := newLocalFixture(t)
	if err := New(f.env(f.primary), "feat", ""); err != nil {
		t.Fatal(err)
	}
	path := f.primary + "--feat"
	commitFile(t, path, "a.txt", "1\n")

	err := Rm(f.env(path), "", false)

	if err == nil || !strings.Contains(err.Error(), "1 commit not merged into main") {
		t.Fatalf("err = %v, want a refusal naming 1 commit not merged into main", err)
	}
}

func TestNew_RefusesAnAllDigitNameBecauseGhReadsItAsAPullRequestNumber(t *testing.T) {
	f := newFixture(t)

	err := New(f.env(f.primary), "1234", "")

	if err == nil || err.Error() != "tree new: a tree name cannot be only digits" {
		t.Fatalf("err = %v, want the digits refusal", err)
	}
	if exists(Path(f.primary, "1234")) {
		t.Errorf("a tree was made for the refused name")
	}
	if err := New(f.env(f.primary), "feat-1234", ""); err != nil {
		t.Errorf("a name with digits in it was refused: %v", err)
	}
}

func TestMerge_LooksUpThePullRequestByHeadNotByName(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "a.txt", "1\n")

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	for _, s := range f.gh.Specs() {
		if len(s.Args) >= 3 && s.Args[0] == "pr" && s.Args[1] == "view" && s.Args[2] == "feat" {
			t.Errorf("gh %v addresses the pull request by branch name", s.Args)
		}
	}
}

func TestNew_RemoteWithoutOriginHeadStopsWithTheSetHeadHint(t *testing.T) {
	f := newFixture(t)
	testiso.Git(t, f.primary, "remote", "set-head", "origin", "-d")

	err := New(f.env(f.primary), "feat", "")

	want := "tree new: origin/HEAD is not set: run git remote set-head origin -a"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if exists(Path(f.primary, "feat")) {
		t.Errorf("a tree was cut from a guessed base")
	}
}

func TestSync_RemotesWithoutOriginAreNamedOnOneLine(t *testing.T) {
	f := newFixture(t)
	testiso.Git(t, f.primary, "remote", "rename", "origin", "upstream")
	testiso.Git(t, f.primary, "remote", "add", "fork", f.origin)

	err := Sync(f.env(f.primary))

	want := "tree sync: no remote named origin (remotes: fork, upstream)"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestNew_ReadsOriginFromAnIncludedConfig(t *testing.T) {
	f := newFixture(t)
	commitFile(t, f.primary, "local.txt", "not pushed\n") // local main is ahead of origin/main
	cfg := filepath.Join(f.primary, ".git", "config")
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var kept, moved []string
	in := false
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(l, "[") {
			in = strings.HasPrefix(l, `[remote "origin"]`)
		}
		if in {
			moved = append(moved, l)
		} else {
			kept = append(kept, l)
		}
	}
	included := filepath.Join(filepath.Dir(f.primary), "remote.cfg")
	testiso.Write(t, included, strings.Join(moved, "\n")+"\n")
	testiso.Write(t, cfg, strings.Join(kept, "\n")+"\n[include]\n\tpath = "+filepath.ToSlash(included)+"\n")

	if err := New(f.env(f.primary), "feat", ""); err != nil {
		t.Fatal(err)
	}

	if exists(filepath.Join(Path(f.primary, "feat"), "local.txt")) {
		t.Errorf("the tree was cut from the local main: the included remote was not seen")
	}
}

// cancelAfter runs the child and then, when its arguments contain every word
// of Verb, cancels the context and reports the child as killed by it; a child
// started on a context already cancelled does not start, as run.Light has it.
type cancelAfter struct {
	Inner  run.Runner
	Verb   []string
	Cancel context.CancelFunc
	Before bool   // cancel before the matching child, which then never starts
	After  func() // called once the matching child has run, before the cancel
}

func (c *cancelAfter) Light(spec run.Spec) (run.Result, error) {
	if spec.Ctx != nil && spec.Ctx.Err() != nil {
		return run.Result{}, spec.Ctx.Err()
	}
	match := spec.Name == "git" && slices.Contains(spec.Args, c.Verb[0]) && slices.Contains(spec.Args, c.Verb[1])
	if match && c.Before {
		c.Cancel()
		return run.Result{}, errors.Join(run.ErrNotStarted, context.Canceled)
	}
	res, err := c.Inner.Light(spec)
	if err == nil && match {
		if c.After != nil {
			c.After()
		}
		c.Cancel()
		return res, fmt.Errorf("git: %w", context.Canceled)
	}
	return res, err
}

func TestMerge_InterruptDuringALocalSquashUndoesItAndLeavesThePrimaryClean(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "one.txt", "1\n")
	commitFile(t, path, "README.md", "tree readme\n")
	before := testiso.Git(t, f.primary, "rev-parse", "HEAD")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := f.env(path)
	env.Ctx = ctx
	env.Runner = &cancelAfter{Inner: f.gh, Verb: []string{"merge", "--squash"}, Cancel: cancel}

	err := Merge(env, "")

	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "the squash in") || !strings.Contains(err.Error(), "was undone") {
		t.Fatalf("err = %v, want an interrupt that names the undone squash", err)
	}
	if got := testiso.Git(t, f.primary, "status", "--porcelain"); got != "" {
		t.Errorf("the primary is not clean after the interrupt:\n%s", got)
	}
	if got := testiso.Git(t, f.primary, "rev-parse", "HEAD"); got != before {
		t.Errorf("the primary moved to %s, want %s", got, before)
	}
	if !exists(path) {
		t.Errorf("the tree was removed by an interrupted merge")
	}
	if slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("an interrupted merge recorded a merge: %v", f.kinds())
	}
}

func TestMerge_InterruptBeforeAnyChildStartsStopsAtOnceWithoutGh(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env := f.env(path)
	env.Ctx = ctx

	err := Merge(env, "")

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls := f.ghCalls(); len(calls) != 0 {
		t.Errorf("gh ran after the interrupt: %v", calls)
	}
	var fx *Fixable
	if errors.As(err, &fx) {
		t.Errorf("an interrupt is not a failure to fix and run again: %v", err)
	}
}

func TestMerge_ABranchCalledDetachedDashSomethingIsNotADetachedHead(t *testing.T) {
	f := newLocalFixture(t)
	if err := New(f.env(f.primary), "detached-fix", ""); err != nil {
		t.Fatal(err)
	}
	path := Path(f.primary, "detached-fix")
	commitFile(t, path, "one.txt", "1\n")

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatalf("a real branch named detached-fix was refused: %v", err)
	}
}

func TestMerge_ARealDetachedHeadIsRefused(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	testiso.Git(t, path, "checkout", "-q", "--detach")

	err := Merge(f.env(path), "")

	if err == nil || err.Error() != "tree merge: HEAD is detached" {
		t.Errorf("err = %v, want the detached refusal", err)
	}
}

func TestMerge_RedCIWithControlBytesInNamesAndLogsPrintsNone(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.watchFail = "x"
	f.checkList = `[{"name":"te\u001b[2Jst\u0007","bucket":"fail","link":"https://github.com/o/r/actions/runs/99/job/5"}]`
	f.logs = map[string]string{"99": "a\tb\t2026-10-18T10:00:01.0000000Z --- FAIL: Test\u001b]0;pwned\u0007A\u0000\n"}

	err := Merge(f.env(path), "")

	want := "tree merge: 1 check did not pass\n" +
		"te[2Jst  https://github.com/o/r/actions/runs/99/job/5\n" +
		"  --- FAIL: Test]0;pwnedA"
	if err == nil || err.Error() != want {
		t.Errorf("error =\n%q\nwant\n%q", err, want)
	}
}

func TestPlain_StripsC0ControlBytesButKeepsNewlineAndTab(t *testing.T) {
	got := plain("a\x00b\x1b[31mc\x07d\re\x7ff\tg\nh\u00e9")

	if want := "ab[31mcde\x7ff\tg\nh\u00e9"; got != want {
		t.Errorf("plain = %q, want %q", got, want)
	}
}

// redCheck answers the shell the --check command runs in with a failure whose
// last line has an escape sequence.
type redCheck struct{ Inner run.Runner }

func (r redCheck) Light(spec run.Spec) (run.Result, error) {
	if spec.Name == "sh" || spec.Name == "cmd" {
		return run.Result{Stderr: []byte("first\nlast \x1b[31mred\x07\n"), ExitCode: 1}, errors.New("exit status 1")
	}
	return r.Inner.Light(spec)
}

func TestMerge_CheckLastLineHasNoControlBytes(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	env := f.env(path)
	env.Runner = redCheck{Inner: f.gh}

	err := Merge(env, "make test")

	want := `tree merge: check "make test" failed: last [31mred`
	if err == nil || err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestMerge_InterruptWithAStaleIndexLockStillLeavesThePrimaryClean(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "one.txt", "1\n")
	commitFile(t, path, "README.md", "tree readme\n")
	lock := filepath.Join(f.primary, ".git", "index.lock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := f.env(path)
	env.Ctx = ctx
	env.Runner = &cancelAfter{Inner: f.gh, Verb: []string{"merge", "--squash"}, Cancel: cancel,
		After: func() { testiso.Write(t, lock, "") }} // what a killed git leaves behind

	err := Merge(env, "")

	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "was undone") {
		t.Fatalf("err = %v, want an interrupt that names the undone squash", err)
	}
	if got := testiso.Git(t, f.primary, "status", "--porcelain"); got != "" {
		t.Errorf("the primary is not clean:\n%s", got)
	}
	if exists(lock) {
		t.Errorf("the stale index.lock was left")
	}
}

func TestMerge_InterruptBeforeTheSquashStartsSaysSo(t *testing.T) {
	f := newLocalFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "one.txt", "1\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := f.env(path)
	env.Ctx = ctx
	env.Runner = &cancelAfter{Inner: f.gh, Verb: []string{"merge", "--squash"}, Cancel: cancel, Before: true}

	err := Merge(env, "")

	if err == nil || err.Error() != "tree merge: interrupted before the squash started" || !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the before-the-squash line wrapping context.Canceled", err)
	}
	if got := testiso.Git(t, f.primary, "status", "--porcelain"); got != "" {
		t.Errorf("the primary is not clean:\n%s", got)
	}
}
