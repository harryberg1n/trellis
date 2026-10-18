package host

import (
	"reflect"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestMain(m *testing.M) { testiso.Main(m) }

// fakeRunner records the specs and answers with Out.
type fakeRunner struct {
	specs []run.Spec
	out   string
}

func (f *fakeRunner) Light(s run.Spec) (run.Result, error) {
	f.specs = append(f.specs, s)
	return run.Result{Stdout: []byte(f.out)}, nil
}

var _ run.Runner = (*fakeRunner)(nil)

func TestGH_ListIssuesArgvAndDecoding(t *testing.T) {
	r := &fakeRunner{out: `[{"number":7,"title":"escape: x","body":"see #3","createdAt":"2026-10-02T10:00:00Z"}]`}

	got, err := GH{Runner: r, Dir: "d"}.ListIssues("", "escape:")

	if err != nil {
		t.Fatal(err)
	}
	want := []Issue{{Number: 7, Title: "escape: x", Body: "see #3", CreatedAt: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("issues = %+v, want %+v", got, want)
	}
	wantArgs := []string{"issue", "list", "--search", "escape:", "--state", "all", "--limit", "500", "--json", "number,title,body,createdAt"}
	if !reflect.DeepEqual(r.specs[0].Args, wantArgs) || r.specs[0].Name != "gh" || r.specs[0].Dir != "d" {
		t.Errorf("spec = %+v, want gh %v in d", r.specs[0], wantArgs)
	}
}

func TestGH_ListMergedPRsArgvAndDecoding(t *testing.T) {
	r := &fakeRunner{out: `[{"number":5,"headRefName":"feat-x"}]`}

	got, err := GH{Runner: r, Dir: "d"}.ListMergedPRs("")

	if err != nil {
		t.Fatal(err)
	}
	want := []PR{{Number: 5, Branch: "feat-x"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prs = %+v, want %+v", got, want)
	}
	wantArgs := []string{"pr", "list", "--state", "merged", "--limit", "500", "--json", "number,headRefName"}
	if !reflect.DeepEqual(r.specs[0].Args, wantArgs) {
		t.Errorf("argv = %v, want %v", r.specs[0].Args, wantArgs)
	}
}

func TestGH_CreateAndEditTargetTheRepoAndKeepTheBodyOffTheArgv(t *testing.T) {
	r := &fakeRunner{out: "https://github.com/o/r/issues/5\n"}
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
	if !reflect.DeepEqual(r.specs[0].Args, wantCreate) || string(r.specs[0].Stdin) != "BODY" {
		t.Errorf("create spec = %+v", r.specs[0])
	}
	if !reflect.DeepEqual(r.specs[1].Args, wantEdit) || string(r.specs[1].Stdin) != "BODY2" {
		t.Errorf("edit spec = %+v", r.specs[1])
	}
}
