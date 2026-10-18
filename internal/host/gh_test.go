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
	if want := []string{"pr", "view", "feat", "--json", "number,state"}; !reflect.DeepEqual(r.Specs()[0].Args, want) {
		t.Errorf("argv = %v, want %v", r.Specs()[0].Args, want)
	}

	none := &testsupport.FakeRunner{Answer: func(run.Spec) (run.Result, error) {
		return run.Result{ExitCode: 1}, errors.New(`no pull requests found for branch "feat"`)
	}}
	if _, err := (GH{Runner: none}).PRView("feat"); !errors.Is(err, ErrNoPR) {
		t.Errorf("err = %v, want ErrNoPR", err)
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
	if err := g.MergePR(7); err != nil {
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
