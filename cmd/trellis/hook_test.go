package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestHook_RecordsAnEditAndPrintsNothing(t *testing.T) {
	bin := build(t)
	repo := testiso.Repo(t)
	state := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"s-1","cwd":` + quote(repo) + `,"hook_event_name":"PostToolUse","tool_name":"Edit",` +
		`"tool_input":{"file_path":` + quote(filepath.Join(repo, "hello.txt")) + `,"old_string":"a","new_string":"b"}}`

	cmd := exec.Command(bin, "hook", "PostToolUse")
	cmd.Env = append(os.Environ(), "TRELLIS_STATE="+state)
	cmd.Stdin = strings.NewReader(payload)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("trellis hook: %v (stderr %q)", err, errb.String())
	}

	if out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("stdout %q stderr %q, want both empty", out.String(), errb.String())
	}
	logs, _ := filepath.Glob(filepath.Join(state, "repos", "*", "events.jsonl"))
	if len(logs) != 1 {
		t.Fatalf("event logs %v, want exactly one under %s", logs, state)
	}
	raw, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind":"edit"`, `"lane":"main"`, `"session":"s-1"`, `"path":"hello.txt"`, `"blob":"ce013625030ba8dba906f756967f9e9ca394464a"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("event log lacks %s:\n%s", want, raw)
		}
	}
}

func TestHook_GarbageStillExitsZero(t *testing.T) {
	bin := build(t)
	cmd := exec.Command(bin, "hook", "Stop")
	cmd.Env = append(os.Environ(), "TRELLIS_STATE="+filepath.Join(t.TempDir(), "state"))
	cmd.Stdin = strings.NewReader("this is not json")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("trellis hook on garbage: %v (stderr %q)", err, errb.String())
	}
	if out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("stdout %q stderr %q, want both empty", out.String(), errb.String())
	}
}

func TestHook_NoEventIsAUsageErrorThatDoesNotBlock(t *testing.T) {
	var out, errb bytes.Buffer
	code := runIn([]string{"hook"}, strings.NewReader(""), &out, &errb)
	if code != 1 || out.Len() != 0 || !strings.Contains(errb.String(), "usage: trellis hook <event>") {
		t.Errorf("exit %d, stdout %q, stderr %q; want 1, empty, the usage line (never 2: exit 2 blocks the agent)", code, out.String(), errb.String())
	}
}

// quote is s as a JSON string.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func TestHook_FallbackLivesInTheUserCacheDir(t *testing.T) {
	bin := build(t)
	repo := testiso.Repo(t)
	cache := t.TempDir()
	badRoot := filepath.Join(t.TempDir(), "root")
	if err := os.WriteFile(badRoot, []byte("a file, not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"s-1","cwd":` + quote(repo) + `,"hook_event_name":"PostToolUse","tool_name":"Edit",` +
		`"tool_input":{"file_path":` + quote(filepath.Join(repo, "hello.txt")) + `}}`

	cmd := exec.Command(bin, "hook", "PostToolUse")
	cmd.Env = append(os.Environ(), "TRELLIS_STATE="+badRoot, "LocalAppData="+cache, "XDG_CACHE_HOME="+cache, "HOME="+cache)
	cmd.Stdin = strings.NewReader(payload)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil || out.Len() != 0 || errb.Len() != 0 {
		t.Fatalf("exit %v, stdout %q, stderr %q; want 0, empty, empty", err, out.String(), errb.String())
	}

	m, _ := filepath.Glob(filepath.Join(cache, "*", "trellis", "fallback", "events.pending", "*.json"))
	m2, _ := filepath.Glob(filepath.Join(cache, "trellis", "fallback", "events.pending", "*.json"))
	if len(m)+len(m2) != 1 {
		t.Errorf("fallback events under the cache dir %s: %v %v; want exactly one", cache, m, m2)
	}
}
