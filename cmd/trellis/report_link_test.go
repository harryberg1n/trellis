package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/measure"
	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/testiso"
)

var linkNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// plainHistory is a repo on main with three commits whose subjects name no
// pull request, as a rebase merge leaves them; it returns the repo and their
// shas, oldest first.
func plainHistory(t *testing.T) (string, []string) {
	t.Helper()
	repo := testiso.Repo(t)
	names := []string{"x.go", "y.go", "z.go"}
	shas := make([]string, 0, len(names))
	for i, name := range names {
		date := fmt.Sprintf("2026-10-0%dT09:00:00Z", 7+i)
		t.Setenv("GIT_COMMITTER_DATE", date)
		testiso.Write(t, filepath.Join(repo, name), "package p\n")
		testiso.Git(t, repo, "add", "-A")
		testiso.Git(t, repo, "commit", "-q", "--date="+date, "-m", "add "+name)
		shas = append(shas, testiso.Git(t, repo, "rev-parse", "HEAD"))
	}
	return repo, shas
}

func TestReport_CountsARebaseMergedTreeFromItsEvent(t *testing.T) {
	repo, shas := plainHistory(t)
	events := []kernel.Event{
		{Kind: kernel.EvTreeMerged, Tree: "feat", Detail: map[string]string{"pr": "20", "merge": shas[2]}},
	}
	r, err := objects.Open(repo)
	if err != nil {
		t.Fatal(err)
	}

	merges, err := readHistory(newGHFake("[]", "[]"), r, linkNow, events)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, m := range merges {
		got[m.Sha] = fmt.Sprintf("%s#%d", m.Tree, m.PR)
	}
	for sha, want := range map[string]string{shas[0]: "#0", shas[1]: "#0", shas[2]: "feat#20"} {
		if got[sha] != want {
			t.Errorf("commit %.7s links to %q, want %q", sha, got[sha], want)
		}
	}
	body := string(measure.Report(measure.Facts{Now: linkNow, Merges: merges, Events: events}))
	if !strings.Contains(body, "trees merged: 1 (defect 0, clean 0, pending 1)") {
		t.Errorf("report:\n%s\nwant one tree merged, pending", body)
	}
}

func TestReport_SquashHistoryUnchanged(t *testing.T) {
	repo := repoWithHistory(t)
	events := []kernel.Event{
		{Kind: kernel.EvTreeMerged, Tree: "other", Detail: map[string]string{"pr": "9", "merge": "0123456789abcdef0123456789abcdef01234567"}},
	}
	r, err := objects.Open(repo)
	if err != nil {
		t.Fatal(err)
	}

	merges, err := readHistory(newGHFake("[]", "[]"), r, linkNow, events)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]int{}
	for _, m := range merges {
		got[m.Subject] = m.PR
		if m.Tree != "" {
			t.Errorf("commit %q has tree %q, want none from a subject alone", m.Subject, m.Tree)
		}
	}
	for subject, want := range map[string]int{"add a (#1)": 1, "add b (#2)": 2, "add c (#3)": 3} {
		if got[subject] != want {
			t.Errorf("%q has PR %d, want %d", subject, got[subject], want)
		}
	}
}
