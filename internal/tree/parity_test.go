package tree

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
)

// pushOtherCommit clones origin, puts one commit on the branch feat there and
// pushes it, as a second person (or a second machine) would.
func (f *fixture) pushOtherCommit(t *testing.T) {
	t.Helper()
	other := filepath.Join(filepath.Dir(f.primary), "other-clone")
	testiso.Git(t, filepath.Dir(f.primary), "clone", "-q", f.origin, other)
	testiso.Git(t, other, "config", "user.name", "Other")
	testiso.Git(t, other, "config", "user.email", "other@example.com")
	testiso.Git(t, other, "checkout", "-q", "-b", "feat", "origin/main")
	commitFile(t, other, "theirs.txt", "theirs\n")
	testiso.Git(t, other, "push", "-q", "origin", "feat")
}

func headOf(t *testing.T, path string) string {
	t.Helper()
	return testiso.Git(t, path, "rev-parse", "HEAD")
}

// 1. The merge is bound to the judged head.

func TestMerge_IsBoundToTheHeadTheChecksJudged(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	head := headOf(t, path)

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	want := []string{"pr", "merge", "7", "--squash", "--match-head-commit", head}
	if got := f.mergeArgs(); !reflect.DeepEqual(got, want) {
		t.Errorf("gh merge argv = %v, want %v", got, want)
	}
}

func TestMerge_HeadModifiedByThePushBetweenJudgementAndMergeIsFixable(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	f.mergeErr = "gh: exit status 1: GraphQL: Head branch was modified. Review and try the merge again. (mergePullRequest)"

	err := Merge(f.env(path), "")

	want := "tree merge: the PR head moved after it was judged (merge bound to " + headOf(t, path)[:12] + "): run trellis tree merge again to judge the new head"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
	if !exists(path) || slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("tree exists %v, kinds %v; want the tree kept and nothing recorded", exists(path), f.kinds())
	}
}

// 3. No checks is not accepted while a run of the head is open.

const (
	runQueued = `[{"headSha":"HEAD","status":"queued"}]`
	runDone   = `[{"headSha":"HEAD","status":"completed"}]`
)

func (f *fixture) runsOnHead(t *testing.T, path string, seq ...string) {
	t.Helper()
	head := headOf(t, path)
	for _, s := range seq {
		f.runSeq = append(f.runSeq, strings.ReplaceAll(s, "HEAD", head))
	}
}

func TestMerge_NoChecksKeepsWaitingWhileARunOfTheHeadIsQueued(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.noChecks = true
	f.runsOnHead(t, path, runQueued, runQueued, runDone)

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if f.clk.afters != 20 {
		t.Errorf("waits on the clock = %d, want 20 (the 18 of the window and 2 for the queued run)", f.clk.afters)
	}
	if !slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("kinds %v, want merged once the run completed", f.kinds())
	}
	wantArgs := []string{"run", "list", "--branch", "feat", "--event", "pull_request", "--limit", "30", "--json", "headSha,status"}
	var got []string
	for _, s := range f.gh.Specs() {
		if s.Args[0] == "run" {
			got = s.Args
		}
	}
	if !reflect.DeepEqual(got, wantArgs) {
		t.Errorf("run list argv = %v, want %v", got, wantArgs)
	}
}

func TestMerge_ARunOfAnotherHeadDoesNotHoldTheNoChecksAnswer(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.noChecks = true
	f.runSeq = []string{`[{"headSha":"ffffffffffffffffffffffffffffffffffffffff","status":"in_progress"}]`}

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if f.clk.afters != 18 {
		t.Errorf("waits on the clock = %d, want 18 (the window only)", f.clk.afters)
	}
}

func TestMerge_AQueuedRunThatNeverStartsEndsTheWaitAfterThirtyMinutes(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.noChecks = true
	f.runsOnHead(t, path, runQueued)

	err := Merge(f.env(path), "")

	want := "tree merge: no checks appeared and a run of this head is still queued or in progress after 1800s: run again"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
	if f.clk.afters != 360 {
		t.Errorf("waits on the clock = %d, want 360 (30 minutes in 5 s steps)", f.clk.afters)
	}
	if slices.Contains(f.ghCalls(), "pr merge") {
		t.Errorf("merged with a run still open")
	}
}

// 4. The remote branch is deleted after the merge.

func TestMerge_DeletesTheBranchOnOriginAfterTheMerge(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if got := testiso.Git(t, f.origin, "branch", "--list", "feat"); got != "" {
		t.Errorf("origin still has feat (%q)", got)
	}
	if !strings.Contains(f.out.String(), "deleted origin/feat\n") {
		t.Errorf("output = %q, want the line deleted origin/feat", f.out.String())
	}
}

func TestMerge_ABranchOriginAlreadyReapedIsSaidNotWarned(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeSHA = "0123456789abcdef0123456789abcdef01234567"
	commitFile(t, path, "mine.txt", "mine\n")
	f.onMerge = func() { testiso.Git(t, f.origin, "branch", "-D", "feat") } // auto-delete-head-branch

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(f.out.String(), "origin/feat is already gone\n") {
		t.Errorf("output = %q, want the line origin/feat is already gone", f.out.String())
	}
	if f.errb.Len() != 0 {
		t.Errorf("warnings = %q, want none", f.errb.String())
	}
	if exists(path) {
		t.Errorf("tree still exists")
	}
}

func TestMerge_ARefusedDeleteOfTheOriginBranchWarnsAndStillRemovesTheTree(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeSHA = "0123456789abcdef0123456789abcdef01234567"
	env := f.env(path)
	env.Runner = failing{Inner: f.gh, Verb: []string{"push", "origin", "--delete"}, Err: "git: exit status 1: remote: Permission to o/r denied"}

	if err := Merge(env, ""); err != nil {
		t.Fatal(err)
	}

	if want := "trellis: origin/feat was not deleted: remote: Permission to o/r denied\n"; f.errb.String() != want {
		t.Errorf("warnings = %q, want %q", f.errb.String(), want)
	}
	if exists(path) || !slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("tree exists %v, kinds %v; want the merge finished", exists(path), f.kinds())
	}
}

// 5. A push never drops commits the tree lacks.

func TestMerge_PushesWithTheLeaseAndForceIfIncludes(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	rec := &run.Recorder{Inner: f.gh}
	env := f.env(path)
	env.Runner = rec

	if err := Merge(env, ""); err != nil {
		t.Fatal(err)
	}

	var push []string
	for _, s := range rec.Specs() {
		if s.Name == "git" && slices.Contains(s.Args, "push") && !slices.Contains(s.Args, "--delete") {
			push = s.Args
		}
	}
	want := []string{"push", "--force-with-lease", "--force-if-includes", "-u", "origin", "feat"}
	if !reflect.DeepEqual(push, want) {
		t.Errorf("push argv = %v, want %v", push, want)
	}
}

func TestMerge_RefusedPushSaysWhatOriginHasAndTheNextStep(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	f.pushOtherCommit(t)

	err := Merge(f.env(path), "")

	want := "tree merge: push refused: origin/feat has 1 commit this tree lacks (1 local not pushed): " +
		"git pull --rebase origin feat, then merge again"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
	if slices.Contains(f.ghCalls(), "pr create") {
		t.Errorf("opened a PR after a refused push")
	}
	if got := testiso.Git(t, f.origin, "log", "--format=%s", "feat"); !strings.Contains(got, "edit theirs.txt") || strings.Contains(got, "mine.txt") {
		t.Errorf("origin feat log = %q, want their commit kept and ours not pushed", got)
	}
}

// 6. Transient gh failures are retried in the checks poll and the watch.

func TestMerge_RetriesATransientChecksReadWithGrowingWaits(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.flaky = map[string][]string{"pr checks --json": {"gh: exit status 1: HTTP 502: Bad Gateway", "gh: exit status 1: unexpected end of JSON input"}}

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if want := []time.Duration{5 * time.Second, 10 * time.Second}; !reflect.DeepEqual(f.clk.waits[:2], want) {
		t.Errorf("first waits = %v, want %v", f.clk.waits[:2], want)
	}
	for _, line := range []string{
		"checks of #7: GitHub could not be reached (1 of 5 tries): HTTP 502: Bad Gateway; trying again in 5s\n",
		"checks of #7: GitHub could not be reached (2 of 5 tries): unexpected end of JSON input; trying again in 10s\n",
	} {
		if !strings.Contains(f.out.String(), line) {
			t.Errorf("output = %q, want the line %q", f.out.String(), line)
		}
	}
	if !slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("kinds %v, want merged after the retries", f.kinds())
	}
}

func TestMerge_GivesUpAfterFiveTransientReadsAndSaysTheChecksCouldNotBeRead(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	boom := "gh: exit status 1: HTTP 503: Service Unavailable"
	f.flaky = map[string][]string{"pr checks --json": {boom, boom, boom, boom, boom, boom}}

	err := Merge(f.env(path), "")

	if want := "tree merge: checks could not be read: HTTP 503: Service Unavailable"; err == nil || fixable(err) != nil || err.Error() != want {
		t.Errorf("err = %v, want %q (exit 1)", err, want)
	}
	if want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second}; !reflect.DeepEqual(f.clk.waits, want) {
		t.Errorf("waits = %v, want %v", f.clk.waits, want)
	}
	if got := len(f.flaky["pr checks --json"]); got != 1 {
		t.Errorf("%d failures left unread, want 1 (five reads of six)", got)
	}
}

func TestMerge_ARateLimitsRetryAfterLongerThanTheBackoffIsWaitedOut(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.flaky = map[string][]string{
		"pr checks --json":  {"gh: exit status 1: HTTP 429: slow down. Retry-After: 30"},
		"pr checks --watch": {"gh: exit status 1: HTTP 429: Retry-After: 9000"},
	}

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if want := []time.Duration{30 * time.Second, 2 * time.Minute}; !reflect.DeepEqual(f.clk.waits, want) {
		t.Errorf("waits = %v, want %v (the Retry-After, capped at two minutes)", f.clk.waits, want)
	}
}

func TestMerge_AWatchThatMeetsA503IsRetried(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.flaky = map[string][]string{"pr checks --watch": {"gh: exit status 1: HTTP 503: Service Unavailable"}}

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(f.out.String(), "checks of #7: GitHub could not be reached (1 of 5 tries): HTTP 503: Service Unavailable; trying again in 5s\n") ||
		!strings.Contains(f.out.String(), "checks passed on #7\n") {
		t.Errorf("output = %q, want the retry line and then checks passed", f.out.String())
	}
}

func TestMerge_ARefusalThatIsNotTransientIsNotRetried(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.flaky = map[string][]string{"pr checks --json": {"gh: exit status 1: HTTP 404: Not Found"}}

	err := Merge(f.env(path), "")

	if err == nil || err.Error() != "tree merge: checks could not be read: HTTP 404: Not Found" {
		t.Errorf("err = %v, want the 404 at once", err)
	}
	if len(f.clk.waits) != 0 {
		t.Errorf("waits = %v, want none", f.clk.waits)
	}
}

// 7. tree new sees a same-named branch on origin.

func TestNew_RefusesABranchThatIsOnOriginAndSaysHowToTrackIt(t *testing.T) {
	f := newFixture(t)
	testiso.Git(t, f.primary, "push", "-q", "origin", "main:refs/heads/shared")

	err := New(f.env(f.primary), "shared", "")

	want := `tree new: branch "shared" exists on origin: git worktree add --track -b shared ` + f.primary + `--shared origin/shared`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if got := testiso.Git(t, f.primary, "branch", "--list", "shared"); got != "" {
		t.Errorf("branch shared was created (%q)", got)
	}
	if exists(f.primary + "--shared") {
		t.Errorf("tree directory was created")
	}
}

// 11. tree sync refuses a dirty tree before fetching.

func TestSync_RefusesATreeWithUncommittedChangesBeforeFetching(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, f.primary, "theirs.txt", "theirs\n")
	testiso.Git(t, f.primary, "push", "-q", "origin", "main")
	before := testiso.Git(t, path, "rev-parse", "origin/main")
	testiso.Write(t, filepath.Join(path, "README.md"), "edited\n")

	err := Sync(f.env(path))

	if want := "tree sync: uncommitted changes: commit or stash, then sync again"; fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
	if after := testiso.Git(t, path, "rev-parse", "origin/main"); after != before {
		t.Errorf("origin/main moved to %s: the sync fetched before it refused", after)
	}
}

func TestSync_AnUntrackedFileDoesNotStopTheRebase(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	testiso.Write(t, filepath.Join(path, "scratch.txt"), "x\n")

	if err := Sync(f.env(path)); err != nil {
		t.Fatalf("sync = %v, want an untracked file to be no reason to refuse", err)
	}
}

// 12. The gh probe tells not installed from not logged in.

func TestMerge_GhNotInstalledIsItsOwnLine(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.gh.Answer = func(run.Spec) (run.Result, error) {
		return run.Result{}, &exec.Error{Name: "gh", Err: exec.ErrNotFound}
	}

	err := Merge(f.env(path), "")

	if want := "tree merge: gh is not installed: install it from https://cli.github.com"; err == nil || fixable(err) != nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestMerge_GhNotLoggedInIsItsOwnLine(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.gh.Answer = func(spec run.Spec) (run.Result, error) {
		if spec.Args[0] == "api" && spec.Args[1] == "user" {
			return run.Result{ExitCode: 4}, errors.New("gh: exit status 4: To get started with GitHub CLI, please run: gh auth login")
		}
		return run.Result{}, nil
	}

	err := Merge(f.env(path), "")

	if want := "tree merge: gh is not logged in: run gh auth login"; err == nil || fixable(err) != nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if got := testiso.Git(t, f.primary, "branch", "--list", "-r", "origin/feat"); got != "" {
		t.Errorf("pushed (%q) though gh was not usable", got)
	}
}

// 13. The --check output streams.

// chatty is a runner whose check writes lines to the writers it is given, as a
// running child would, and then fails.
type chatty struct{ Inner run.Runner }

func (c chatty) Light(spec run.Spec) (run.Result, error) {
	if spec.Name == "sh" || spec.Name == "cmd" {
		_, _ = spec.Stdout.Write([]byte("compiling\n"))
		_, _ = spec.Stderr.Write([]byte("FAIL TestX\nlast words \x1b[31mred\n"))
		return run.Result{ExitCode: 1}, errors.New("exit status 1")
	}
	return c.Inner.Light(spec)
}

func TestMerge_CheckOutputStreamsToTheVerbAndTheLastLineIsTheSummary(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	env := f.env(path)
	env.Runner = chatty{Inner: f.gh}

	err := Merge(env, "make test")

	if want := "tree merge: check \"make test\" failed: last words [31mred"; fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %q, want fixable %q (the escape byte taken out)", err, want)
	}
	if want := "compiling\nFAIL TestX\nlast words \x1b[31mred\n"; f.out.String() != want {
		t.Errorf("output = %q, want the child's lines as written, %q", f.out.String(), want)
	}
}

func TestMerge_ARealCheckStreamsItsOutput(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)

	err := Merge(f.env(path), "echo one&& echo two 1>&2&& exit 1")

	if want := `tree merge: check "echo one&& echo two 1>&2&& exit 1" failed: two`; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	got := strings.Fields(f.out.String())
	if !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Errorf("output words = %q, want [one two]", got)
	}
}

// 14. Tree names are validated.

func TestNew_RefusesNamesGitCannotUseAsABranch(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"-x", "--detach", "a b", "a..b", "feat/", "x.lock", "~x", "a:b", "a?b", "@", "a//b"} {
		err := New(f.env(f.primary), name, "")
		if want := fmt.Sprintf("tree new: %q is not a valid branch name", name); err == nil || err.Error() != want {
			t.Errorf("name %q: err = %v, want %q", name, err, want)
		}
	}
	if got := testiso.Git(t, f.primary, "branch", "--list"); strings.Count(got, "\n") != 0 {
		t.Errorf("branches = %q, want only main", got)
	}
	if err := New(f.env(f.primary), "feat/ok-1.2", ""); err != nil {
		t.Errorf("a good name was refused: %v", err)
	}
}

// 2. Each command has the deadline of what it does.

func TestNew_FetchHasTwoMinutesAndWorktreeAddTen(t *testing.T) {
	f := newFixture(t)
	rec := &run.Recorder{Inner: f.gh}
	env := f.env(f.primary)
	env.Runner = rec

	if err := New(env, "feat", ""); err != nil {
		t.Fatal(err)
	}

	got := map[string]time.Duration{}
	for _, s := range rec.Specs() {
		got[run.Kind(s)] = s.Timeout
	}
	if got["fetch"] != 120*time.Second || got["worktree"] != 10*time.Minute {
		t.Errorf("timeouts = %v, want fetch 2m0s and worktree 10m0s", got)
	}
}

func TestMerge_APushThatTimedOutSaysSo(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	env := f.env(path)
	env.Runner = failingWith{Inner: f.gh, Verb: "push", Err: errors.Join(run.ErrTimeout, errors.New("signal: killed"))}

	err := Merge(env, "")

	if err == nil || !strings.Contains(err.Error(), "git push: timed out after 120s") {
		t.Errorf("err = %v, want it to name git push and 120s", err)
	}
}

// failingWith fails the git children of one verb with err.
type failingWith struct {
	Inner run.Runner
	Verb  string
	Err   error
}

func (f failingWith) Light(spec run.Spec) (run.Result, error) {
	if spec.Name == "git" && run.Kind(spec) == f.Verb {
		return run.Result{}, f.Err
	}
	return f.Inner.Light(spec)
}

// 16. The wait lines carry the UTC time.

func TestMerge_WaitLinesStartWithTheClocksTimeInUTC(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.clk.now = time.Date(2026, 10, 18, 15, 45, 9, 0, time.FixedZone("CEST", 2*3600))

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{"13:45:09 pull request #7\n", "13:45:09 checks passed on #7\n"} {
		if !strings.Contains(f.out.String(), line) {
			t.Errorf("output = %q, want the line %q", f.out.String(), line)
		}
	}
	if !strings.HasPrefix(f.out.String(), "pushed feat\n") {
		t.Errorf("output = %q, want pushed unstamped: it is no wait", f.out.String())
	}
}

// 17. Red-check lines are read per job.

func TestMerge_TwoFailedJobsOfOneRunEachShowTheirOwnLines(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.watchFail = "x"
	f.checkList = `[{"name":"test-windows","bucket":"fail","link":"https://github.com/o/r/actions/runs/99/job/5"},` +
		`{"name":"test-linux","bucket":"fail","link":"https://github.com/o/r/actions/runs/99/job/6"}]`
	f.logs = map[string]string{
		"5": "test-windows\tRun\t2026-10-18T10:00:01.0000000Z --- FAIL: TestWriteSettings_PublishesThroughATempFileInTheSameDirectory (0.00s)\n",
		"6": "test-linux\tRun\t2026-10-18T10:00:01.0000000Z --- FAIL: TestInstall_CreatesOwnerOnlyFilesAndDirectories (0.00s)\n",
		"99": "test-windows\tRun\t2026-10-18T10:00:01.0000000Z --- FAIL: TestWriteSettings_PublishesThroughATempFileInTheSameDirectory (0.00s)\n" +
			"test-linux\tRun\t2026-10-18T10:00:01.0000000Z --- FAIL: TestInstall_CreatesOwnerOnlyFilesAndDirectories (0.00s)\n",
	}

	err := Merge(f.env(path), "")

	want := "tree merge: 2 checks did not pass\n" +
		"test-windows  https://github.com/o/r/actions/runs/99/job/5\n" +
		"  --- FAIL: TestWriteSettings_PublishesThroughATempFileInTheSameDirectory (0.00s)\n" +
		"test-linux  https://github.com/o/r/actions/runs/99/job/6\n" +
		"  --- FAIL: TestInstall_CreatesOwnerOnlyFilesAndDirectories (0.00s)"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err =\n%v\nwant\n%s", err, want)
	}
}

func TestMerge_ACheckWhoseLinkNamesNoJobFallsBackToTheRunsLog(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.watchFail = "x"
	f.checkList = `[{"name":"lint","bucket":"fail","link":"https://github.com/o/r/actions/runs/98"}]`
	f.logs = map[string]string{"98": "lint\tRun\t2026-10-18T10:00:01.0000000Z Error: bad style\n"}

	err := Merge(f.env(path), "")

	want := "tree merge: 1 check did not pass\nlint  https://github.com/o/r/actions/runs/98\n  Error: bad style"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err =\n%v\nwant\n%s", err, want)
	}
}

// A fetch (tree sync does one) refreshes origin/feat, which makes the lease
// pass; only --force-if-includes still knows the tree never took the commit.
func TestMerge_AnOriginCommitThatWasFetchedButNeverTakenIsNotOverwritten(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	f.pushOtherCommit(t)
	testiso.Git(t, path, "fetch", "-q", "origin")

	err := Merge(f.env(path), "")

	want := "tree merge: push refused: origin/feat has 1 commit this tree lacks (1 local not pushed): " +
		"git pull --rebase origin feat, then merge again"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
	if got := testiso.Git(t, f.origin, "log", "--format=%s", "feat"); !strings.Contains(got, "edit theirs.txt") || strings.Contains(got, "mine.txt") {
		t.Errorf("origin feat log = %q, want their commit kept", got)
	}
}
