package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestHook_RecordsAnEditAndPrintsNothing(t *testing.T) {
	bin := build(t)
	repo := markedRepo(t)
	state := filepath.Join(t.TempDir(), "state")
	payload := helloEdit(t, repo)

	code, stdout, stderr := runBin(t, bin, state, string(payload), []string{"hook", "PostToolUse"})
	if code != 0 {
		t.Fatalf("trellis hook: exit %d (stderr %q)", code, stderr)
	}

	if stdout != "" || stderr != "" {
		t.Errorf("stdout %q stderr %q, want both empty", stdout, stderr)
	}
	logs, _ := filepath.Glob(filepath.Join(state, "repos", "*", "events.jsonl"))
	if len(logs) != 1 {
		t.Fatalf("event logs %v, want exactly one under %s", logs, state)
	}
	raw, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind":"edit"`, `"tree":"main"`, `"session":"s-1"`, `"path":"hello.txt"`, `"blob":"ce013625030ba8dba906f756967f9e9ca394464a"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("event log lacks %s:\n%s", want, raw)
		}
	}
}

func TestHook_GarbageStillExitsZero(t *testing.T) {
	bin := build(t)
	code, stdout, stderr := runBin(t, bin, filepath.Join(t.TempDir(), "state"), "this is not json", []string{"hook", "Stop"})
	if code != 0 || stdout != "" || stderr != "" {
		t.Errorf("trellis hook on garbage: exit %d, stdout %q stderr %q; want 0, both empty", code, stdout, stderr)
	}
}

func TestHook_NoEventIsAUsageErrorThatDoesNotBlock(t *testing.T) {
	var out, errb bytes.Buffer
	code := runIn([]string{"hook"}, strings.NewReader(""), &out, &errb)
	if code != 1 || out.Len() != 0 || !strings.Contains(errb.String(), "usage: trellis hook <event>") {
		t.Errorf("exit %d, stdout %q, stderr %q; want 1, empty, the usage line (never 2: exit 2 blocks the agent)", code, out.String(), errb.String())
	}
}

func TestHook_FallbackLivesBesideTheStateRootInTheUserCacheDir(t *testing.T) {
	bin := build(t)
	repo := markedRepo(t)
	cache := t.TempDir()
	badRoot := filepath.Join(t.TempDir(), "root")
	if err := os.WriteFile(badRoot, []byte("a file, not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := helloEdit(t, repo)

	code, stdout, stderr := runBin(t, bin, badRoot, string(payload), []string{"hook", "PostToolUse"},
		withEnv("LocalAppData="+cache, "XDG_CACHE_HOME="+cache, "HOME="+cache))
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q; want 0, empty, empty", code, stdout, stderr)
	}

	m, _ := filepath.Glob(filepath.Join(cache, "trellis-fallback", "*", "events.pending", "*.json"))
	if len(m) != 1 {
		t.Errorf("fallback events under %s/trellis-fallback/<repo id>/events.pending: %v; want exactly one", cache, m)
	}
	if in, _ := filepath.Glob(filepath.Join(cache, "trellis", "*")); len(in) != 0 {
		t.Errorf("%v: the fallback must sit beside the state root, not inside it", in)
	}
}

// markedRepo is a repository that has opted in: trellis.toml is committed at
// its root.
func markedRepo(t *testing.T) string {
	t.Helper()
	repo := testiso.Repo(t)
	testiso.Write(t, filepath.Join(repo, "trellis.toml"), "# trellis is on in this repository; commit this file\n")
	testiso.Git(t, repo, "add", "trellis.toml")
	testiso.Git(t, repo, "commit", "-q", "-m", "trellis on")
	return repo
}

// fileTree lists every file under dir with the sha256 of its content, "" when
// dir is not there, so two calls compare a directory's whole state.
func fileTree(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		sum := [32]byte{}
		if !d.IsDir() {
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			sum = sha256.Sum256(data)
		}
		fmt.Fprintf(&b, "%s %x\n", p, sum)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return b.String()
}

func TestHook_OffHereWritesNothingAndSaysNothingOnEveryInstalledEvent(t *testing.T) {
	bin := build(t)
	unmarked := testiso.Repo(t)
	marked := markedRepo(t)
	outside := t.TempDir()
	stateRoot := filepath.Join(t.TempDir(), "state")
	cache := t.TempDir()
	cases := []struct {
		name    string
		payload string
		env     []string
		dir     string
	}{
		{"a repository without trellis.toml", string(helloEdit(t, unmarked)), nil, outside},
		{"TRELLIS_OFF=1", string(helloEdit(t, marked)), []string{"TRELLIS_OFF=1"}, outside},
		{"a payload outside any repository", string(testiso.HookPayload(t, kernel.HookStop, "", outside, nil)), nil, outside},
		{"no cwd, the process outside any repository", `{"hook_event_name":"Stop","session_id":"s-1"}`, nil, outside},
	}
	events := kernel.InstalledHookEvents()
	if len(events) != 6 {
		t.Fatalf("%d installed events %v, want 6", len(events), events)
	}
	for _, event := range events {
		for _, c := range cases {
			name := event + ", " + c.name
			beforeState, beforeCache, beforeRepos := fileTree(t, stateRoot), fileTree(t, cache), fileTree(t, marked)+fileTree(t, unmarked)
			env := append([]string{"LocalAppData=" + cache, "XDG_CACHE_HOME=" + cache, "HOME=" + cache}, c.env...)
			code, stdout, stderr := runBin(t, bin, stateRoot, c.payload, []string{"hook", event}, withEnv(env...), inDir(c.dir))
			if code != 0 || stdout != "" || stderr != "" {
				t.Errorf("%s: exit %d, stdout %q, stderr %q; want 0, empty, empty", name, code, stdout, stderr)
			}
			if got := fileTree(t, stateRoot); got != beforeState || got != "" {
				t.Errorf("%s: the state root changed or exists:\n%s", name, got)
			}
			if got := fileTree(t, cache); got != beforeCache {
				t.Errorf("%s: the cache dir changed:\n%s", name, got)
			}
			if _, err := os.Stat(filepath.Join(cache, "trellis-fallback")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s: the fallback dir exists (%v), want it absent", name, err)
			}
			if got := fileTree(t, marked) + fileTree(t, unmarked); got != beforeRepos {
				t.Errorf("%s: a repository changed", name)
			}
		}
	}
}

// storeIDOf records one edit from repo into a fresh state root and returns the
// id of the one store directory it made, and the root.
func storeIDOf(t *testing.T, bin, repo string) (id, state string) {
	t.Helper()
	state = filepath.Join(t.TempDir(), "state")
	code, stdout, stderr := runBin(t, bin, state, string(helloEdit(t, repo)), []string{"hook", "PostToolUse"}, inDir(t.TempDir()))
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("%s: exit %d, stdout %q, stderr %q", repo, code, stdout, stderr)
	}
	logs, _ := filepath.Glob(filepath.Join(state, "repos", "*", "events.jsonl"))
	if len(logs) != 1 {
		t.Fatalf("%s: event logs %v, want exactly one", repo, logs)
	}
	return filepath.Base(filepath.Dir(logs[0])), state
}

func TestHook_ATreeWithTheCommittedMarkerRecordsToThePrimarysStore(t *testing.T) {
	bin := build(t)
	primary := markedRepo(t)
	tree := testiso.AddTree(t, primary, "tree", "tree/x")

	primaryID, _ := storeIDOf(t, bin, primary)
	treeID, treeState := storeIDOf(t, bin, tree)
	if treeID != primaryID {
		t.Fatalf("the tree wrote to store %s, the primary to %s; want one store", treeID, primaryID)
	}

	// Both in one state root: one log, two edits.
	code, _, _ := runBin(t, bin, treeState, string(helloEdit(t, primary)), []string{"hook", "PostToolUse"}, inDir(t.TempDir()))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	raw, err := os.ReadFile(filepath.Join(treeState, "repos", primaryID, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), `"kind":"edit"`); n != 2 {
		t.Errorf("%d edit events in the one log, want 2 (one from the tree, one from the primary):\n%s", n, raw)
	}
}

func TestHook_APayloadWithoutCwdIsRecordedFromTheProcessWorkingDirectory(t *testing.T) {
	bin := build(t)
	repo := markedRepo(t)
	file := filepath.Join(repo, "hello.txt")
	testiso.Write(t, file, "hello\n")
	payload := `{"session_id":"s-1","hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":` +
		testiso.JSONString(file) + `,"old_string":"a","new_string":"b"}}`
	state := filepath.Join(t.TempDir(), "state")

	code, stdout, stderr := runBin(t, bin, state, payload, []string{"hook", "PostToolUse"}, inDir(repo))

	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	logs, _ := filepath.Glob(filepath.Join(state, "repos", "*", "events.jsonl"))
	if len(logs) != 1 {
		t.Fatalf("event logs %v, want one: the event is recorded", logs)
	}
	raw, _ := os.ReadFile(logs[0])
	if !strings.Contains(string(raw), `"kind":"edit"`) || strings.Contains(string(raw), "internal.error") {
		t.Errorf("log:\n%s", raw)
	}
}
