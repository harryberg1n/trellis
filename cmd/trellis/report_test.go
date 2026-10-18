package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/objects"
	runpkg "github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/testiso"
	"github.com/harryberg1n/trellis/internal/testsupport"
)

// ghFake answers gh itself and sends everything else (git) to the real
// runner. issues and prs are what any `gh issue list` and `gh pr list` return.
type ghFake struct {
	*testsupport.FakeRunner
	issues string
	prs    string
}

func newGHFake(issues, prs string) *ghFake {
	g := &ghFake{issues: issues, prs: prs}
	g.FakeRunner = &testsupport.FakeRunner{Name: "gh", Inner: testsupport.Recorder(), Answer: g.answer}
	return g
}

func (g *ghFake) answer(s runpkg.Spec) (runpkg.Result, error) {
	switch s.Args[1] {
	case "list":
		if s.Args[0] == "pr" {
			return runpkg.Result{Stdout: []byte(g.prs)}, nil
		}
		return runpkg.Result{Stdout: []byte(g.issues)}, nil
	case "create":
		return runpkg.Result{Stdout: []byte("https://github.com/harryberg1n/trellis/issues/77\n")}, nil
	}
	return runpkg.Result{}, nil
}

// repoWithHistory is a repo on main: "add a (#1)" on 2026-10-01, a revert of
// it on the 2nd and "add b (#2)" on the 3rd and
// "add c (#3)", authored on 09-27 and committed on 10-07.
func repoWithHistory(t *testing.T) string {
	t.Helper()
	repo := testiso.Repo(t)
	commit := func(author, committer, subject, body string) string {
		t.Setenv("GIT_COMMITTER_DATE", committer)
		testiso.Git(t, repo, "add", "-A")
		msg := subject
		if body != "" {
			msg += "\n\n" + body
		}
		testiso.Git(t, repo, "commit", "-q", "--date="+author, "-m", msg)
		return testiso.Git(t, repo, "rev-parse", "HEAD")
	}
	testiso.Write(t, filepath.Join(repo, "a.go"), "package a\n\nvar A = 1\n")
	first := commit("2026-10-01T09:00:00Z", "2026-10-01T09:00:00Z", "add a (#1)", "")
	testiso.Write(t, filepath.Join(repo, "a.go"), "package a\n")
	commit("2026-10-02T09:00:00Z", "2026-10-02T09:00:00Z", `Revert "add a (#1)"`, "This reverts commit "+first+".")
	testiso.Write(t, filepath.Join(repo, "b.go"), "package b\n")
	commit("2026-10-03T09:00:00Z", "2026-10-03T09:00:00Z", "add b (#2)", "")
	testiso.Write(t, filepath.Join(repo, "c.go"), "package c\n")
	// Rebased: authored ten days before it landed on the 7th.
	commit("2026-09-27T09:00:00Z", "2026-10-07T09:00:00Z", "add c (#3)", "")
	return repo
}

const sentinel = "SENTINEL-PROMPT-7f3a"

// reportFixture builds a repo whose history has tree A (#1) reverted and tree
// B (#2) recent, an event log that opened them, and a transcript with a
// sentinel in it.
func reportFixture(t *testing.T) (reportEnv, *ghFake, string) {
	t.Helper()
	return reportFixtureWithRemote(t, "https://example.invalid/repo.git")
}

// reportFixtureWithRemote is reportFixture with origin set to remote, or no
// remote at all when remote is empty.
func reportFixtureWithRemote(t *testing.T, remote string) (reportEnv, *ghFake, string) {
	t.Helper()
	repo := repoWithHistory(t)
	if remote != "" {
		testiso.Git(t, repo, "remote", "add", "origin", remote)
	}
	stateRoot := t.TempDir()

	c := clock.Fake(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	st, err := store.Open(store.RepoDir(stateRoot, store.RepoID(remote, repo)), c)
	if err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	testiso.Write(t, transcript, `{"type":"user","timestamp":"2026-10-01T09:00:00Z","message":{"content":"`+sentinel+`"}}`+"\n"+
		`{"type":"assistant","timestamp":"2026-10-01T09:00:05Z","requestId":"q","message":{"model":"claude-opus-5-5","content":[{"type":"text","text":"`+sentinel+`"}],"usage":{"input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":5000}}}`+"\n")
	at := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	_, err = st.Append(
		kernel.Event{Kind: kernel.EvTreeOpened, Tree: "A", Session: "s1", At: at(1, 9), Detail: map[string]string{"pr": "1"}},
		kernel.Event{Kind: kernel.EvTreeOpened, Tree: "B", Session: "s2", At: at(3, 9), Detail: map[string]string{"pr": "2"}},
		kernel.Event{Kind: kernel.EvTreeOpened, Tree: "C", Session: "s3", At: at(7, 8)},
		kernel.Event{Kind: kernel.EvSessionUsage, Tree: "A", Session: "s1", At: at(1, 11), Detail: map[string]string{"transcript": transcript}},
	)
	if err != nil {
		t.Fatal(err)
	}
	gh := newGHFake("[]", `[{"number":3,"headRefName":"C"}]`)
	return reportEnv{Repo: repo, Root: stateRoot, Runner: gh, Clock: c}, gh, transcript
}

func TestReport_GathersLabelsFromTheRepoHistory(t *testing.T) {
	env, _, _ := reportFixture(t)
	var out, errb bytes.Buffer

	code := reportCmd(nil, &out, &errb, env)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{
		"trees merged: 3 (defect 1, clean 0, pending 2)",
		"trees with usage: 1  total $0.01250",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}

func TestReport_MergeTimeIsTheCommitDate(t *testing.T) {
	env, _, _ := reportFixture(t)
	var out, errb bytes.Buffer

	code := reportCmd(nil, &out, &errb, env)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	// Tree C opened on 10-07 08:00 and was authored on 09-27 but committed on
	// 10-07 09:00: 60 minutes, in the commit date's week 2026-W41.
	if want := "  2026-W41  n=1  p50=60.0  p90=60.0\n"; !strings.Contains(out.String(), want) {
		t.Errorf("report lacks %q:\n%s", want, out.String())
	}
}

func TestReportIssue_TargetsTrellisRepoOnly(t *testing.T) {
	env, gh, transcript := reportFixture(t)
	var out, errb bytes.Buffer

	code := reportCmd([]string{"--issue"}, &out, &errb, env)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	// The first gh call reads the consumer's escape issues; every other one
	// names the trellis repo.
	if len(gh.Specs()) != 4 {
		t.Fatalf("gh calls = %d, want 3 (escape list, pr list, title list, create): %+v", len(gh.Specs()), gh.Specs())
	}
	for _, s := range gh.Specs()[:2] {
		if slicesContain(s.Args, "--repo") {
			t.Errorf("a read of the consumer repo names a repo: %v", s.Args)
		}
	}
	for _, s := range gh.Specs()[2:] {
		if !hasPair(s.Args, "--repo", "harryberg1n/trellis") {
			t.Errorf("gh %v lacks --repo harryberg1n/trellis", s.Args)
		}
	}
	create := gh.Specs()[3]
	wantTitle := "report 2bcb29a3 2026-W41"
	if !hasPair(create.Args, "--title", wantTitle) {
		t.Errorf("create argv = %v, want --title %q", create.Args, wantTitle)
	}
	body := string(create.Stdin)
	for _, bad := range []string{env.Repo, filepath.ToSlash(env.Repo), transcript, filepath.ToSlash(transcript), sentinel, "a.go", "b.go"} {
		if strings.Contains(body, bad) {
			t.Errorf("issue body contains %q:\n%s", bad, body)
		}
	}
	if !strings.Contains(body, "trees merged: 3") {
		t.Errorf("issue body is not the report:\n%s", body)
	}
	if got, want := out.String(), "filed https://github.com/harryberg1n/trellis/issues/77\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestReportIssue_EditsThisWeeksIssue(t *testing.T) {
	env, gh, _ := reportFixture(t)
	title := "report 2bcb29a3 2026-W41"
	gh.issues = fmt.Sprintf(`[{"number":9,"title":%q,"body":"old","createdAt":"2026-10-06T00:00:00Z"}]`, title)
	var out, errb bytes.Buffer

	code := reportCmd([]string{"--issue"}, &out, &errb, env)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	last := gh.Specs()[len(gh.Specs())-1]
	want := []string{"issue", "edit", "9", "--repo", "harryberg1n/trellis", "--body-file", "-"}
	if !reflect.DeepEqual(last.Args, want) {
		t.Errorf("last gh call = %v, want %v", last.Args, want)
	}
	if got, want := out.String(), "updated harryberg1n/trellis#9\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func slicesContain(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func hasPair(args []string, k, v string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == k && args[i+1] == v {
			return true
		}
	}
	return false
}

func TestReport_OpensTheStoreTheHookWrote_WhateverTheGlobalConfigSays(t *testing.T) {
	env, _, _ := reportFixtureWithRemote(t, "")
	// The hook reads the repo's own config file only. A remote that only the
	// user's global config names must not move the report to another store.
	testiso.Git(t, env.Repo, "config", "--global", "remote.origin.url", "https://example.invalid/global.git")
	var out, errb bytes.Buffer

	code := reportCmd(nil, &out, &errb, env)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if want := "trees with usage: 1  total $0.01250"; !strings.Contains(out.String(), want) {
		t.Errorf("report lacks %q (an empty log?):\n%s", want, out.String())
	}
}

func TestReport_ReadsOriginsHistoryWhenTheLocalDefaultLags(t *testing.T) {
	env, _, _ := reportFixture(t)
	testiso.Git(t, env.Repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	testiso.Git(t, env.Repo, "update-ref", "refs/heads/main", "HEAD~2")
	var out, errb bytes.Buffer

	code := reportCmd(nil, &out, &errb, env)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if want := "trees merged: 3"; !strings.Contains(out.String(), want) {
		t.Errorf("report lacks %q: the lagging local main hid merges:\n%s", want, out.String())
	}
}

func TestReport_ReadsHEADWhenThereIsNoMainOrMaster(t *testing.T) {
	env, _, _ := reportFixture(t)
	testiso.Git(t, env.Repo, "branch", "-m", "trunk")
	var out, errb bytes.Buffer

	code := reportCmd(nil, &out, &errb, env)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if want := "trees merged: 3"; !strings.Contains(out.String(), want) {
		t.Errorf("report lacks %q:\n%s", want, out.String())
	}
}

func TestReport_NoRemoteSkipsGitHubReads(t *testing.T) {
	env, gh, _ := reportFixtureWithRemote(t, "")
	var out, errb bytes.Buffer

	code := reportCmd(nil, &out, &errb, env)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if n := len(gh.Specs()); n != 0 {
		t.Errorf("gh was called %d times with no remote: %+v", n, gh.Specs())
	}
	if want := "trellis report: no remote: issues and pull requests not read\n"; errb.String() != want {
		t.Errorf("stderr = %q, want %q", errb.String(), want)
	}
	if want := "trees merged: 3"; !strings.Contains(out.String(), want) {
		t.Errorf("report lacks %q:\n%s", want, out.String())
	}
}

// appendEvents adds events to the log of the fixture's repository.
func appendEvents(t *testing.T, env reportEnv, evs ...kernel.Event) {
	t.Helper()
	st, err := storeIn(env.Root, mustRepo(t, env.Repo), clock.Fake(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(evs...); err != nil {
		t.Fatal(err)
	}
}

func TestReport_ChargesUsageToTheTreeCurrentAtEachStop(t *testing.T) {
	env, _, _ := reportFixture(t)
	reply := func(id, at string) string {
		return `{"type":"assistant","timestamp":"` + at + `","requestId":"` + id + `","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":5000}}}` + "\n"
	}
	long := filepath.Join(t.TempDir(), "long.jsonl")
	testiso.Write(t, long, reply("a", "2026-10-02T09:00:05Z")+reply("b", "2026-10-02T09:10:05Z"))
	at := func(m int) time.Time { return time.Date(2026, 10, 2, 9, m, 0, 0, time.UTC) }
	stop := func(tree string, m int) kernel.Event {
		return kernel.Event{Kind: kernel.EvTurnAnchored, Tree: tree, Session: "long", At: at(m), Detail: map[string]string{"transcript": long}}
	}
	appendEvents(t, env, stop("A", 5), stop("B", 15))
	var out, errb bytes.Buffer

	if code := reportCmd(nil, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	// s1's session.usage (tree A, 0.0125) plus the long session split in two:
	// A and B each have usage.
	for _, want := range []string{"trees with usage: 2  total $0.03750"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "transcripts missing") {
		t.Errorf("report names missing transcripts, but every one was read:\n%s", out.String())
	}
}

func TestReport_CountsSessionsWithNoReadableTranscript(t *testing.T) {
	env, _, _ := reportFixture(t)
	gone := filepath.Join(t.TempDir(), "gone.jsonl")
	at := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	appendEvents(t, env,
		kernel.Event{Kind: kernel.EvTurnAnchored, Tree: "A", Session: "killed", At: at, Detail: map[string]string{"transcript": gone}},
		kernel.Event{Kind: kernel.EvSessionUsage, Tree: "B", Session: "nopath", At: at},
	)
	var out, errb bytes.Buffer

	if code := reportCmd(nil, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	if want := "  transcripts missing: 2\n"; !strings.Contains(out.String(), want) {
		t.Errorf("report lacks %q:\n%s", want, out.String())
	}
}

func TestReportIssue_TitleCarriesTheRepoIDNotItsName(t *testing.T) {
	env, gh, _ := reportFixtureWithRemote(t, "https://example.invalid/private-client-x.git")
	var out, errb bytes.Buffer

	if code := reportCmd([]string{"--issue"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	create := gh.Specs()[len(gh.Specs())-1]
	// sha256 of the remote URL, first 8 hex digits.
	if want := "report 3d561701 2026-W41"; !hasPair(create.Args, "--title", want) {
		t.Errorf("create argv = %v, want --title %q", create.Args, want)
	}
	for _, s := range gh.Specs() {
		for _, a := range s.Args {
			if strings.Contains(a, "private-client-x") {
				t.Errorf("gh argv %v carries the repo name", s.Args)
			}
		}
	}
}

func mustRepo(t *testing.T, dir string) objects.Repo {
	t.Helper()
	r, err := objects.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReport_ReadsTheHistoryInOneGitCall(t *testing.T) {
	env, gh, _ := reportFixture(t)
	var out, errb bytes.Buffer

	if code := reportCmd(nil, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	var logs, shows int
	for _, s := range gh.Inner.(*runpkg.Recorder).Specs() {
		for _, a := range s.Args {
			switch a {
			case "log":
				logs++
			case "show":
				shows++
			}
		}
	}
	if logs != 1 || shows != 0 {
		t.Errorf("history took %d git log and %d git show calls, want 1 and 0", logs, shows)
	}
}
