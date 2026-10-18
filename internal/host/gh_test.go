package host

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
	"github.com/harryberg1n/trellis/internal/testsupport"
)

func TestMain(m *testing.M) { testiso.Main(m) }

func TestGH_ListIssuesArgvAndDecoding(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout(`[{"number":7,"title":"escape: x","body":"see #3","createdAt":"2026-10-02T10:00:00Z"}]`)}

	got, err := GH{Runner: r, Dir: "d"}.ListIssues("", "escape:")
	if err != nil {
		t.Fatal(err)
	}
	want := []Issue{{Number: 7, Title: "escape: x", Body: "see #3", CreatedAt: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("issues = %+v, want %+v", got, want)
	}
	wantArgs := []string{"issue", "list", "--search", "escape:", "--state", "all", "--limit", "500", "--json", "number,title,body,createdAt"}
	if !reflect.DeepEqual(r.Specs()[0].Args, wantArgs) || r.Specs()[0].Name != "gh" || r.Specs()[0].Dir != "d" {
		t.Errorf("spec = %+v, want gh %v in d", r.Specs()[0], wantArgs)
	}
}

func TestGH_ListMergedPRsArgvAndDecoding(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout(`[{"number":5,"headRefName":"feat-x"}]`)}

	got, err := GH{Runner: r, Dir: "d"}.ListMergedPRs("")
	if err != nil {
		t.Fatal(err)
	}
	want := []PR{{Number: 5, Branch: "feat-x"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prs = %+v, want %+v", got, want)
	}
	wantArgs := []string{"pr", "list", "--state", "merged", "--limit", "500", "--json", "number,headRefName"}
	if !reflect.DeepEqual(r.Specs()[0].Args, wantArgs) {
		t.Errorf("argv = %v, want %v", r.Specs()[0].Args, wantArgs)
	}
}

func TestGH_PRViewAddressesThePullRequestByItsHeadNeverByName(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout(`[{"number":7,"state":"OPEN"}]`)}
	got, err := GH{Runner: r, Dir: "d"}.PRView("123")
	if err != nil || got != (PRState{Number: 7, State: "OPEN"}) {
		t.Fatalf("PRView = %+v, %v; want #7 OPEN", got, err)
	}
	want := []string{"pr", "list", "--head", "123", "--state", "all", "--limit", "30", "--json", "number,state,mergeable,mergeStateStatus,headRefOid,reviewDecision"}
	if !reflect.DeepEqual(r.Specs()[0].Args, want) {
		t.Errorf("argv = %v, want %v", r.Specs()[0].Args, want)
	}
}

func TestGH_PRViewPrefersTheOpenPullRequestOfAHead(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout(`[{"number":9,"state":"CLOSED"},{"number":8,"state":"OPEN"},{"number":3,"state":"MERGED"}]`)}

	got, err := GH{Runner: r}.PRView("feat")

	if err != nil || got.Number != 8 {
		t.Errorf("PRView = %+v, %v; want #8, the open one", got, err)
	}
	r = &testsupport.FakeRunner{Answer: testsupport.Stdout(`[{"number":9,"state":"MERGED"},{"number":3,"state":"CLOSED"}]`)}
	if got, err := (GH{Runner: r}).PRView("feat"); err != nil || got.Number != 9 {
		t.Errorf("PRView = %+v, %v; want #9, the newest", got, err)
	}
}

func TestGH_PRViewNamesAHeadWithNoPullRequest(t *testing.T) {
	none := &testsupport.FakeRunner{Answer: testsupport.Stdout("[]")}

	_, err := GH{Runner: none}.PRView("feat")

	if !errors.Is(err, ErrNoPR) {
		t.Errorf("err = %v, want ErrNoPR", err)
	}
}

func TestGH_ViewMergeableRepollsUnknownAndStopsAtTheBound(t *testing.T) {
	reads := 0
	answers := []string{"UNKNOWN", "UNKNOWN", "CONFLICTING"}
	r := &testsupport.FakeRunner{Answer: func(run.Spec) (run.Result, error) {
		a := answers[min(reads, len(answers)-1)]
		reads++
		return run.Result{Stdout: []byte(`[{"number":7,"state":"OPEN","mergeable":"` + a + `","mergeStateStatus":"DIRTY"}]`)}, nil
	}}
	waits := 0
	pr, err := GH{Runner: r}.ViewMergeable("feat", func(d time.Duration) {
		waits++
		if d != 750*time.Millisecond {
			t.Errorf("delay = %v, want 750ms", d)
		}
	})
	if err != nil || !pr.IsConflicting() || reads != 3 || waits != 2 {
		t.Errorf("pr %+v, err %v, reads %d, waits %d; want conflicting after 3 reads and 2 waits", pr, err, reads, waits)
	}

	reads, answers = 0, []string{"UNKNOWN"}
	pr, err = GH{Runner: r}.ViewMergeable("feat", func(time.Duration) {})
	if err != nil || !pr.MergeUnknown() || reads != 4 {
		t.Errorf("pr %+v, err %v, reads %d; want UNKNOWN returned after 4 reads", pr, err, reads)
	}
}

func TestGH_ChecksReadsTheListThoughGhExitsNonZero(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: func(run.Spec) (run.Result, error) {
		return run.Result{Stdout: []byte(`[{"name":"test","bucket":"fail","link":"L"}]`), ExitCode: 1}, errors.New("exit status 1")
	}}
	got, err := GH{Runner: r}.Checks(7)
	if err != nil || !reflect.DeepEqual(got, []Check{{Name: "test", Bucket: "fail", Link: "L"}}) {
		t.Errorf("Checks = %+v, %v", got, err)
	}
}

func TestGH_CreateWatchAndMergeArgv(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout("out\n")}
	g := GH{Runner: r, Dir: "d"}
	if url, err := g.CreatePR("main"); err != nil || url != "out" {
		t.Fatalf("CreatePR = %q, %v", url, err)
	}
	if _, err := g.WatchChecks(7); err != nil {
		t.Fatal(err)
	}
	if err := g.MergePR(7, MergeRebase); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"pr", "create", "--fill-first", "--base", "main"},
		{"pr", "checks", "7", "--watch"},
		{"pr", "merge", "7", "--rebase"},
	}
	for i, s := range r.Specs() {
		if !reflect.DeepEqual(s.Args, want[i]) {
			t.Errorf("call %d argv = %v, want %v", i, s.Args, want[i])
		}
	}
	if got := r.Specs()[1].Timeout; got != 45*time.Minute {
		t.Errorf("watch timeout = %v, want 45m", got)
	}
}

func TestGH_CreateAndEditTargetTheRepoAndKeepTheBodyOffTheArgv(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout("https://github.com/o/r/issues/5\n")}
	g := GH{Runner: r}

	url, err := g.CreateIssue("o/r", "t", "BODY")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.EditIssue("o/r", 5, "BODY2"); err != nil {
		t.Fatal(err)
	}

	if url != "https://github.com/o/r/issues/5" {
		t.Errorf("url = %q", url)
	}
	wantCreate := []string{"issue", "create", "--repo", "o/r", "--title", "t", "--body-file", "-"}
	wantEdit := []string{"issue", "edit", "5", "--repo", "o/r", "--body-file", "-"}
	if !reflect.DeepEqual(r.Specs()[0].Args, wantCreate) || string(r.Specs()[0].Stdin) != "BODY" {
		t.Errorf("create spec = %+v", r.Specs()[0])
	}
	if !reflect.DeepEqual(r.Specs()[1].Args, wantEdit) || string(r.Specs()[1].Stdin) != "BODY2" {
		t.Errorf("edit spec = %+v", r.Specs()[1])
	}
}

func TestGH_ChecksOfAPullRequestWithNoneYetIsEmpty(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: func(run.Spec) (run.Result, error) {
		return run.Result{ExitCode: 1}, errors.New("exit status 1: no checks reported on the 'feat' branch")
	}}

	got, err := GH{Runner: r, Dir: "d"}.Checks(7)

	if err != nil || len(got) != 0 {
		t.Errorf("checks = %v, err = %v; want none and no error", got, err)
	}
}

func TestGH_WorkflowCountArgvAndDecoding(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout("3\n")}

	got, err := GH{Runner: r, Dir: "d"}.WorkflowCount()

	wantArgs := []string{"api", "repos/{owner}/{repo}/actions/workflows", "--jq", ".total_count"}
	if err != nil || got != 3 || !reflect.DeepEqual(r.Specs()[0].Args, wantArgs) {
		t.Errorf("count = %d, err = %v, args = %v; want 3 from %v", got, err, r.Specs()[0].Args, wantArgs)
	}
}

func TestGH_MergeCommitArgvAndDecoding(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout(`{"mergeCommit":{"oid":"abc123"}}`)}

	got, err := GH{Runner: r, Dir: "d"}.MergeCommit(20)

	wantArgs := []string{"pr", "view", "20", "--json", "mergeCommit"}
	if err != nil || got != "abc123" || !reflect.DeepEqual(r.Specs()[0].Args, wantArgs) {
		t.Errorf("sha = %q, err = %v, args = %v; want abc123 from %v", got, err, r.Specs()[0].Args, wantArgs)
	}
}

func TestGH_MergedPRsArgvAndDecoding(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout(`[{"number":5,"headRefOid":"aaa","baseRefName":"main","mergeCommit":{"oid":"bbb"}},{"number":4,"headRefOid":"ccc","baseRefName":"dev","mergeCommit":null}]`)}

	got, err := GH{Runner: r, Dir: "d"}.MergedPRs("feat")

	want := []string{"pr", "list", "--head", "feat", "--state", "merged", "--limit", "30", "--json", "number,headRefOid,baseRefName,mergeCommit"}
	if err != nil || len(got) != 2 || !reflect.DeepEqual(r.Specs()[0].Args, want) {
		t.Fatalf("prs = %+v, err = %v, args = %v; want two from %v", got, err, r.Specs()[0].Args, want)
	}
	if got[0].HeadSHA != "aaa" || got[0].BaseBranch != "main" || got[0].MergeSHA() != "bbb" || got[1].MergeSHA() != "" {
		t.Errorf("decoded %+v", got)
	}
}

func TestGH_ListIssuesWithoutASearchPassesNone(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout(`[]`)}

	if _, err := (GH{Runner: r, Dir: "d"}).ListIssues("o/r", ""); err != nil {
		t.Fatal(err)
	}

	want := []string{"issue", "list", "--repo", "o/r", "--state", "all", "--limit", "500", "--json", "number,title,body,createdAt"}
	if got := r.Specs()[0].Args; !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func TestGH_MergeBoundToTheJudgedHeadNeverDeletesTheBranchItself(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout("")}
	g := GH{Runner: r}

	if err := g.MergePR(7, MergeSquash, MatchHead("0123abc")); err != nil {
		t.Fatal(err)
	}

	want := []string{"pr", "merge", "7", "--squash", "--match-head-commit", "0123abc"}
	if got := r.Specs()[0].Args; !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func TestGH_MergeRefusedForAMovedHeadIsErrHeadModified(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: func(run.Spec) (run.Result, error) {
		return run.Result{ExitCode: 1}, errors.New("gh: exit status 1: pull request o/r#7: head branch was modified, review and try the merge again")
	}}

	err := GH{Runner: r}.MergePR(7, MergeSquash, MatchHead("0123abc"))

	if !errors.Is(err, ErrHeadModified) {
		t.Errorf("err = %v, want ErrHeadModified", err)
	}
	r = &testsupport.FakeRunner{Answer: func(run.Spec) (run.Result, error) {
		return run.Result{ExitCode: 1}, errors.New("exit status 1: GraphQL: Pull request is in clean status")
	}}
	if err := (GH{Runner: r}).MergePR(7, MergeSquash); errors.Is(err, ErrHeadModified) || err == nil {
		t.Errorf("err = %v, want a plain failure", err)
	}
}

func TestGH_ReadyTellsNotInstalledFromNotLoggedIn(t *testing.T) {
	missing := &testsupport.FakeRunner{Answer: func(run.Spec) (run.Result, error) {
		return run.Result{}, &exec.Error{Name: "gh", Err: exec.ErrNotFound}
	}}
	if err := (GH{Runner: missing}).Ready(); !errors.Is(err, ErrNotInstalled) || errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("gh missing: err = %v, want ErrNotInstalled only", err)
	}
	loggedOut := &testsupport.FakeRunner{Answer: func(run.Spec) (run.Result, error) {
		return run.Result{ExitCode: 4}, errors.New("exit status 4: gh: To use GitHub CLI, run: gh auth login")
	}}
	if err := (GH{Runner: loggedOut}).Ready(); !errors.Is(err, ErrNotLoggedIn) || errors.Is(err, ErrNotInstalled) {
		t.Errorf("gh logged out: err = %v, want ErrNotLoggedIn only", err)
	}
	if got, want := loggedOut.Specs()[0].Args, []string{"api", "user"}; !reflect.DeepEqual(got, want) {
		t.Errorf("probe argv = %v, want %v", got, want)
	}
	ok := &testsupport.FakeRunner{Answer: testsupport.Stdout("{}")}
	if err := (GH{Runner: ok}).Ready(); err != nil {
		t.Errorf("Ready = %v, want nil", err)
	}
}

func TestGH_RunsOnTheBranchReadsHeadAndStatus(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout(`[{"headSha":"aaa","status":"queued"},{"headSha":"bbb","status":"completed"}]`)}

	got, err := GH{Runner: r}.PullRequestRuns("feat/a")

	want := []Run{{HeadSHA: "aaa", Status: "queued"}, {HeadSHA: "bbb", Status: "completed"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("runs = %+v, %v; want %+v", got, err, want)
	}
	wantArgs := []string{"run", "list", "--branch", "feat/a", "--event", "pull_request", "--limit", "30", "--json", "headSha,status"}
	if !reflect.DeepEqual(r.Specs()[0].Args, wantArgs) {
		t.Errorf("argv = %v, want %v", r.Specs()[0].Args, wantArgs)
	}
}

func TestGH_FailedJobLogAsksForTheJobNotTheRun(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout("log")}

	out, err := GH{Runner: r}.FailedJobLog("55")

	want := []string{"run", "view", "--job", "55", "--log-failed"}
	if err != nil || out != "log" || !reflect.DeepEqual(r.Specs()[0].Args, want) {
		t.Errorf("out %q err %v argv %v; want log and %v", out, err, r.Specs()[0].Args, want)
	}
}

func TestGH_ATimeoutNamesTheCommandAndTheDeadline(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: func(run.Spec) (run.Result, error) {
		return run.Result{}, errors.Join(run.ErrTimeout, errors.New("signal: killed"))
	}}

	_, err := GH{Runner: r}.Checks(7)

	if err == nil || !strings.Contains(err.Error(), "gh pr checks: timed out after 60s") {
		t.Errorf("err = %v, want it to say gh pr checks: timed out after 60s", err)
	}
	if !errors.Is(err, run.ErrTimeout) {
		t.Errorf("err = %v, want it to still wrap run.ErrTimeout", err)
	}
}
