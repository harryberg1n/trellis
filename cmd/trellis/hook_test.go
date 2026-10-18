package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestHook_RecordsAnEditAndPrintsNothing(t *testing.T) {
	bin := build(t)
	repo := testiso.Repo(t)
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
	repo := testiso.Repo(t)
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

	m, _ := filepath.Glob(filepath.Join(cache, "*", "trellis-fallback", "*", "events.pending", "*.json"))
	m2, _ := filepath.Glob(filepath.Join(cache, "trellis-fallback", "*", "events.pending", "*.json"))
	if len(m)+len(m2) != 1 {
		t.Errorf("fallback events under the cache dir %s: %v %v; want exactly one", cache, m, m2)
	}
	if in, _ := filepath.Glob(filepath.Join(cache, "trellis", "*")); len(in) != 0 {
		t.Errorf("%v: the fallback must sit beside the state root, not inside it", in)
	}
}
