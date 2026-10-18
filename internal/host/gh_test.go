package host

import (
	"errors"
	"reflect"
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

func TestGH_PRViewDecodesAndNamesAMissingPR(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout(`{"number":7,"state":"OPEN"}`)}
	got, err := GH{Runner: r, Dir: "d"}.PRView("feat")
	if err != nil || got != (PRState{Number: 7, State: "OPEN"}) {
		t.Fatalf("PRView = %+v, %v; want #7 OPEN", got, err)
	}
	if want := []string{"pr", "view", "feat", "--json", "number,state,mergeable,mergeStateStatus,headRefOid,reviewDecision"}; !reflect.DeepEqual(r.Specs()[0].Args, want) {
		t.Errorf("argv = %v, want %v", r.Specs()[0].Args, want)
	}

	none := &testsupport.FakeRunner{Answer: func(run.Spec) (run.Result, error) {
		return run.Result{ExitCode: 1}, errors.New(`no pull requests found for branch "feat"`)
	}}
	if _, err := (GH{Runner: none}).PRView("feat"); !errors.Is(err, ErrNoPR) {
		t.Errorf("err = %v, want ErrNoPR", err)
	}
}

func TestGH_ViewMergeableRepollsUnknownAndStopsAtTheBound(t *testing.T) {
	reads := 0
	answers := []string{"UNKNOWN", "UNKNOWN", "CONFLICTING"}
	r := &testsupport.FakeRunner{Answer: func(run.Spec) (run.Result, error) {
		a := answers[min(reads, len(answers)-1)]
		reads++
		return run.Result{Stdout: []byte(`{"number":7,"state":"OPEN","mergeable":"` + a + `","mergeStateStatus":"DIRTY"}`)}, nil
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
		{"pr", "create", "--fill", "--base", "main"},
		{"pr", "checks", "7", "--watch"},
		{"pr", "merge", "7", "--rebase", "--delete-branch"},
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
