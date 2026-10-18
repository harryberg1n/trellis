package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
	runpkg "github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestReport_NoRemoteIsInTheBodyToo(t *testing.T) {
	env, _, _ := reportFixtureWithRemote(t, "")
	var out, errb bytes.Buffer

	if code := reportCmd(nil, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	if want := "trellis report\nnot read: issues and pull requests: no remote\ntrees merged: 3"; !strings.Contains(out.String(), want) {
		t.Errorf("report lacks %q:\n%s", want, out.String())
	}
}

func TestReport_AFailedGitHubReadIsInTheBody(t *testing.T) {
	env, gh, _ := reportFixture(t)
	gh.fail = func(s runpkg.Spec) error {
		if s.Args[0] == "pr" {
			return errors.New("boom")
		}
		return nil
	}
	var out, errb bytes.Buffer

	if code := reportCmd(nil, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	if want := "not read: merged pull requests\n"; !strings.Contains(out.String(), want) {
		t.Errorf("report lacks %q:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), "boom") {
		t.Errorf("the body carries the error text, which may name a path or a host:\n%s", out.String())
	}
	if strings.Contains(out.String(), "not read: escape issues") {
		t.Errorf("the escape issues were read:\n%s", out.String())
	}
}

func TestReportIssue_ModelIdsAreSizesInTheFiledBodyOnly(t *testing.T) {
	env, gh, _ := reportFixture(t)
	odd := filepath.Join(t.TempDir(), "odd.jsonl")
	testiso.Write(t, odd, `{"type":"assistant","timestamp":"2026-10-01T09:00:05Z","requestId":"z","message":{"model":"claude-opus-9-1","usage":{"input_tokens":40,"output_tokens":2}}}`+"\n")
	appendTranscript(t, env, "odd", "A", odd)
	var local, filed, errb bytes.Buffer

	if code := reportCmd(nil, &local, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if code := reportCmd([]string{"--issue"}, &filed, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	if want := "(models: claude-opus-9-1)"; !strings.Contains(local.String(), want) {
		t.Errorf("the local report lacks %q:\n%s", want, local.String())
	}
	specs := gh.Specs()
	body := string(specs[len(specs)-1].Stdin)
	if want := "(models: large model)"; !strings.Contains(body, want) {
		t.Errorf("the filed body lacks %q:\n%s", want, body)
	}
	if strings.Contains(body, "claude") {
		t.Errorf("the filed body names a model id:\n%s", body)
	}
}

func TestReport_ChargesSubagentTranscriptsBesideTheSession(t *testing.T) {
	env, _, transcript := reportFixture(t)
	sub := filepath.Join(strings.TrimSuffix(transcript, ".jsonl"), "subagents")
	reply := func(id string) string {
		return `{"type":"assistant","timestamp":"2026-10-01T09:30:05Z","requestId":"` + id + `","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":5000}}}` + "\n"
	}
	testiso.Write(t, filepath.Join(sub, "agent-a1.jsonl"), reply("sub1"))
	testiso.Write(t, filepath.Join(sub, "agent-a2.jsonl"), reply("sub2")+reply("q")) // q is the main session's own reply
	var out, errb bytes.Buffer

	if code := reportCmd(nil, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	// main q 0.0125 + sub1 0.0125 + sub2 0.0125; the repeated q counts once.
	if want := "trees with usage: 1  total $0.03750"; !strings.Contains(out.String(), want) {
		t.Errorf("report lacks %q:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), "not read") {
		t.Errorf("report says something was not read:\n%s", out.String())
	}
}

func TestReport_ACorruptSubagentFileIsCounted(t *testing.T) {
	env, _, transcript := reportFixture(t)
	sub := filepath.Join(strings.TrimSuffix(transcript, ".jsonl"), "subagents")
	if err := os.MkdirAll(filepath.Join(sub, "agent-dir.jsonl"), 0o755); err != nil { // cannot be read as a file
		t.Fatal(err)
	}
	var out, errb bytes.Buffer

	if code := reportCmd(nil, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	if want := "  transcript files not read: 1\n"; !strings.Contains(out.String(), want) {
		t.Errorf("report lacks %q:\n%s", want, out.String())
	}
}

func TestReportIssue_FindsTheWeeksIssueByListingNeverBySearch(t *testing.T) {
	env, gh, _ := reportFixture(t)
	var out, errb bytes.Buffer

	if code := reportCmd([]string{"--issue"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	list := gh.Specs()[2]
	if slicesContain(list.Args, "--search") {
		t.Errorf("the title lookup searches: %v", list.Args)
	}
	if !hasPair(list.Args, "--state", "all") || !hasPair(list.Args, "--repo", "harryberg1n/trellis") {
		t.Errorf("the title lookup is not a listing of all issues on the report repo: %v", list.Args)
	}
}

func TestReportIssue_RefusesToOpenWhenTheListingFails(t *testing.T) {
	env, gh, _ := reportFixture(t)
	gh.fail = func(s runpkg.Spec) error {
		if s.Args[1] == "list" && hasPair(s.Args, "--repo", "harryberg1n/trellis") {
			return errors.New("rate limited")
		}
		return nil
	}
	var out, errb bytes.Buffer

	code := reportCmd([]string{"--issue"}, &out, &errb, env)

	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	for _, s := range gh.Specs() {
		if s.Args[1] == "create" || s.Args[1] == "edit" {
			t.Errorf("gh %v ran after the listing failed", s.Args)
		}
	}
	if !strings.Contains(errb.String(), "rate limited") {
		t.Errorf("stderr = %q, want the listing error", errb.String())
	}
}

// appendTranscript logs a session usage event naming path for session on tree.
func appendTranscript(t *testing.T, env reportEnv, session, tree, path string) {
	t.Helper()
	appendEvents(t, env, kernel.Event{
		Kind: kernel.EvSessionUsage, Tree: tree, Session: session,
		At: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Detail: map[string]string{"transcript": path},
	})
}

func TestReport_ANonJSONSubagentFileCountsItsLines(t *testing.T) {
	env, _, transcript := reportFixture(t)
	sub := filepath.Join(strings.TrimSuffix(transcript, ".jsonl"), "subagents")
	testiso.Write(t, filepath.Join(sub, "agent-junk.jsonl"), "this is not json\n")
	var out, errb bytes.Buffer

	if code := reportCmd(nil, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	if want := "  transcript lines skipped: 1\n"; !strings.Contains(out.String(), want) {
		t.Errorf("report lacks %q:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), "files not read") {
		t.Errorf("a readable file is counted as not read:\n%s", out.String())
	}
}

func TestReport_AMissingSessionFileIsCountedAsAFileAndAsAMissingSession(t *testing.T) {
	env, _, _ := reportFixture(t)
	gone := filepath.Join(t.TempDir(), "gone.jsonl")
	appendTranscript(t, env, "killed", "B", gone)
	var out, errb bytes.Buffer

	if code := reportCmd(nil, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	for _, want := range []string{"  transcripts missing: 1\n", "  transcript files not read: 1\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}
