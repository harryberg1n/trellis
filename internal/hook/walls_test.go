package hook

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/testsupport"
)

// pre runs one PreToolUse Bash payload and returns the exit code and what it
// printed.
func (e *env) pre(t testing.TB, cwd, command string, session bool) (code int, stdout, stderr string) {
	t.Helper()
	m := map[string]any{"cwd": cwd, "hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]string{"command": command}}
	if session {
		m["session_id"] = "s-1"
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code = Run("PreToolUse", bytes.NewReader(raw), &out, &errb, e.deps)
	return code, out.String(), errb.String()
}

// lane is a linked worktree of e.repo with one modified tracked file.
func (e *env) dirtyLane(t testing.TB) string {
	t.Helper()
	lane := filepath.Join(filepath.Dir(e.repo), "lane")
	testsupport.Git(t, e.repo, "worktree", "add", "-q", "-b", "lane/x", lane)
	testsupport.Write(t, filepath.Join(lane, "README.md"), "changed\n")
	return lane
}

func details(evs []kernel.Event) []map[string]string {
	var out []map[string]string
	for _, ev := range evs {
		d := map[string]string{"kind": ev.Kind}
		for k, v := range ev.Detail {
			d[k] = v
		}
		out = append(out, d)
	}
	return out
}

func TestPreToolUse_DenyPrintsTheLineExits2AndRecords(t *testing.T) {
	e := newEnv(t)
	testsupport.FakeTool(t, "gitleaks", 0, "")
	code, stdout, stderr := e.pre(t, e.repo, "git commit -m wip", true)
	line := "trellis: main-commit: git commit in the primary checkout, which is on main. Override: trellis allow main-commit."
	if code != 2 || stdout != "" || stderr != line+"\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q; want 2, empty, %q", code, stdout, stderr, line+"\n")
	}
	want := []map[string]string{{"kind": "wall.denied", "row": "main-commit", "line": line, "bytes": strconv.Itoa(len(line))}}
	evs := e.events(t)
	if got := details(evs); !reflect.DeepEqual(got, want) {
		t.Errorf("events %v, want %v", got, want)
	}
	if evs[0].Session != "s-1" || evs[0].Lane != "main" {
		t.Errorf("event session %q lane %q, want s-1 main", evs[0].Session, evs[0].Lane)
	}
}

func TestPreToolUse_APolicyDenialIsPolicyDenied(t *testing.T) {
	e := newEnv(t)
	lane := e.dirtyLane(t)
	testsupport.FakeTool(t, "gitleaks", 0, "")
	code, _, stderr := e.pre(t, lane, "git commit -m \"x\n\nCo-Authored-By: A <a@b>\"", true)
	if code != 2 {
		t.Fatalf("exit %d (stderr %q), want 2", code, stderr)
	}
	evs := e.events(t)
	if len(evs) != 1 || evs[0].Kind != "policy.denied" || evs[0].Detail["row"] != "undercover" {
		t.Errorf("events %v, want one policy.denied from undercover", details(evs))
	}
}

func TestPreToolUse_AllowPrintsNothingAndRecordsNothing(t *testing.T) {
	e := newEnv(t)
	for _, cmd := range []string{"ls", "git status", `echo "git reset --hard"`} {
		if code, stdout, stderr := e.pre(t, e.repo, cmd, true); code != 0 || stdout != "" || stderr != "" {
			t.Errorf("%q: exit %d, stdout %q, stderr %q; want 0, empty, empty", cmd, code, stdout, stderr)
		}
	}
	if evs := e.events(t); len(evs) != 0 {
		t.Errorf("events %v, want none", details(evs))
	}
}

func TestOverride_OneShot(t *testing.T) {
	e := newEnv(t)
	lane := e.dirtyLane(t)
	if err := e.st.Allow("discard-uncommitted"); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{0, 2} {
		code, _, stderr := e.pre(t, lane, "git reset --hard", true)
		if code != want {
			t.Fatalf("run %d: exit %d (stderr %q), want %d: allow, then deny", i+1, code, stderr, want)
		}
	}
	line := "trellis: discard-uncommitted: reset --hard discards 1 unstaged file. Override: trellis allow discard-uncommitted."
	want := []map[string]string{
		{"kind": "wall.overridden", "row": "discard-uncommitted"},
		{"kind": "wall.denied", "row": "discard-uncommitted", "line": line, "bytes": strconv.Itoa(len(line))},
	}
	if got := details(e.events(t)); !reflect.DeepEqual(got, want) {
		t.Errorf("events %v, want %v", got, want)
	}
}

func TestOverride_NotForSecretsOrUndercover(t *testing.T) {
	e := newEnv(t)
	lane := e.dirtyLane(t)
	testsupport.FakeTool(t, "gitleaks", 1, "leaks found: 1")
	for _, row := range []string{"secrets", "undercover"} {
		if err := e.st.Allow(row); err != nil {
			t.Fatal(err)
		}
	}
	if code, _, _ := e.pre(t, lane, `git commit -m "add keys"`, true); code != 2 {
		t.Errorf("secrets with a token: exit %d, want 2: the row has no override", code)
	}
	if code, _, _ := e.pre(t, lane, "git commit -m \"x\n\nCo-Authored-By: A <a@b>\"", true); code != 2 {
		t.Errorf("undercover with a token: exit %d, want 2: the row has no override", code)
	}
	for _, row := range []string{"secrets", "undercover"} {
		if ok, err := e.st.TakeAllow(row); err != nil || !ok {
			t.Errorf("token for %s was consumed (%v, %v): a row with no override leaves it alone", row, ok, err)
		}
	}
}

func TestPreToolUse_WarnGoesToTheAgentAsContextAndRecordsNothing(t *testing.T) {
	e := newEnv(t)
	testsupport.FakeTool(t, "gitleaks", 0, "")
	code, stdout, stderr := e.pre(t, e.repo, "git commit -m wip", false)
	line := "trellis: main-commit: git commit in the primary checkout, which is on main. Override: trellis allow main-commit."
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":` + strconv.Quote(line) + `}}` + "\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q; want 0, %q, empty", code, stdout, stderr, want)
	}
	if evs := e.events(t); len(evs) != 0 {
		t.Errorf("events %v, want none: a warn records nothing", details(evs))
	}
}

func TestPreToolUse_DenyStandsWithoutAStore(t *testing.T) {
	e := newEnv(t)
	testsupport.FakeTool(t, "gitleaks", 0, "")
	e.deps.Store = nil
	e.deps.Fallback = ""
	code, _, stderr := e.pre(t, e.repo, "git commit -m wip", true)
	if code != 2 || stderr == "" {
		t.Errorf("exit %d, stderr %q; want 2 and the deny line: a missing store never lets a wall down", code, stderr)
	}
}

func TestPreToolUse_GarbageStillExitsZero(t *testing.T) {
	e := newEnv(t)
	var out, errb bytes.Buffer
	if code := Run("PreToolUse", bytes.NewReader([]byte("not json")), &out, &errb, e.deps); code != 0 || out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("exit %d, stdout %q, stderr %q; want 0, empty, empty", code, out.String(), errb.String())
	}
}
