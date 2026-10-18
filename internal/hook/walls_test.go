package hook

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"

	"github.com/harryberg1n/trellis/internal/kernel"
)

// pre runs one PreToolUse Bash payload and returns the exit code and what it
// printed.
func (e *env) pre(t testing.TB, cwd, command string, session bool) (code int, stdout, stderr string) {
	t.Helper()
	var opts []testiso.PayloadOption
	if session {
		opts = append(opts, testiso.WithSession("s-1"))
	} else {
		opts = append(opts, testiso.AsPerson())
	}
	raw := testiso.HookPayload(t, kernel.HookPreToolUse, kernel.ToolBash, cwd, map[string]string{"command": command}, opts...)
	var out, errb bytes.Buffer
	code = Run("PreToolUse", bytes.NewReader(raw), &out, &errb, e.deps)
	return code, out.String(), errb.String()
}

// tree is a linked worktree of e.repo with one modified tracked file.
func (e *env) dirtyTree(t testing.TB) string {
	t.Helper()
	tree := testiso.AddTree(t, e.repo, "tree", "tree/x")
	testiso.Write(t, filepath.Join(tree, "README.md"), "changed\n")
	return tree
}

func details(evs []kernel.Event) []map[string]string {
	out := make([]map[string]string, 0, len(evs))
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
	testiso.FakeTool(t, "gitleaks", 0, "")
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
	if evs[0].Session != "s-1" || evs[0].Tree != "main" {
		t.Errorf("event session %q tree %q, want s-1 main", evs[0].Session, evs[0].Tree)
	}
}

func TestPreToolUse_APolicyDenialIsPolicyDenied(t *testing.T) {
	e := newEnv(t)
	tree := e.dirtyTree(t)
	testiso.FakeTool(t, "gitleaks", 0, "")
	code, _, stderr := e.pre(t, tree, "git commit -m \"x\n\nCo-Authored-By: A <a@b>\"", true)
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
	tree := e.dirtyTree(t)
	if err := e.st.Allow("discard-uncommitted"); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{0, 2} {
		code, _, stderr := e.pre(t, tree, "git reset --hard", true)
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
	tree := e.dirtyTree(t)
	testiso.FakeTool(t, "gitleaks", 2, "leaks found: 1")
	for _, row := range []string{"secrets", "undercover"} {
		if err := e.st.Allow(row); err != nil {
			t.Fatal(err)
		}
	}
	if code, _, _ := e.pre(t, tree, `git commit -m "add keys"`, true); code != 2 {
		t.Errorf("secrets with a token: exit %d, want 2: the row has no override", code)
	}
	if code, _, _ := e.pre(t, tree, "git commit -m \"x\n\nCo-Authored-By: A <a@b>\"", true); code != 2 {
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
	testiso.FakeTool(t, "gitleaks", 0, "")
	code, stdout, stderr := e.pre(t, e.repo, "git commit -m wip", false)
	line := "trellis: main-commit: git commit in the primary checkout, which is on main. Override: trellis allow main-commit."
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":` + testiso.JSONString(line) + `}}` + "\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q; want 0, %q, empty", code, stdout, stderr, want)
	}
	if evs := e.events(t); len(evs) != 0 {
		t.Errorf("events %v, want none: a warn records nothing", details(evs))
	}
}

func TestPreToolUse_DenyStandsWithoutAStore(t *testing.T) {
	e := newEnv(t)
	testiso.FakeTool(t, "gitleaks", 0, "")
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

func TestContextJSON_IsValidJSONForControlCharacters(t *testing.T) {
	line := "trellis: x: a\x1bb\ac d <e> & \"f\"\\g"
	var got struct {
		Out struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(contextJSON([]string{line, "second"})), &got); err != nil {
		t.Fatalf("contextJSON is not JSON: %v", err)
	}
	if got.Out.Event != "PreToolUse" || got.Out.Context != line+"\nsecond" {
		t.Errorf("decoded %+v, want PreToolUse and the two lines joined by a newline", got.Out)
	}
}

func TestPreToolUse_AnAllowWithALineIsShownAsContext(t *testing.T) {
	e := newEnv(t)
	tree := e.dirtyTree(t)
	testiso.PathWithOnly(t, "git")
	code, stdout, stderr := e.pre(t, tree, `git commit -m "fix"`, true)
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"not applicable: gitleaks not installed"}}` + "\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q; want 0, %q, empty", code, stdout, stderr, want)
	}
	if evs := e.events(t); len(evs) != 0 {
		t.Errorf("events %v, want none: the line is shown, not recorded", details(evs))
	}
}

func TestOverride_NeedsATokenForEveryDeny(t *testing.T) {
	e := newEnv(t)
	testiso.FakeTool(t, "gitleaks", 0, "")
	cmd := "git commit --no-verify -m x" // main-commit and hook-bypass, in the primary on main
	if err := e.st.Allow("main-commit"); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := e.pre(t, e.repo, cmd, true)
	if code != 2 || !strings.Contains(stderr, "trellis: main-commit: ") || !strings.Contains(stderr, "trellis: hook-bypass: ") {
		t.Fatalf("one token for two denies: exit %d, stderr %q; want 2 and both lines", code, stderr)
	}
	if ok, err := e.st.TakeAllow("main-commit"); err != nil || !ok {
		t.Fatalf("the main-commit token was burnt although the command stayed blocked (%v, %v)", ok, err)
	}
	for _, id := range []string{"main-commit", "hook-bypass"} {
		if err := e.st.Allow(id); err != nil {
			t.Fatal(err)
		}
	}
	if code, _, stderr := e.pre(t, e.repo, cmd, true); code != 0 || stderr != "" {
		t.Fatalf("a token for each deny: exit %d, stderr %q; want 0", code, stderr)
	}
	var overridden []string
	for _, ev := range e.events(t) {
		if ev.Kind == "wall.overridden" {
			overridden = append(overridden, ev.Detail["row"])
		}
	}
	if !reflect.DeepEqual(overridden, []string{"main-commit", "hook-bypass"}) {
		t.Errorf("wall.overridden rows %v, want [main-commit hook-bypass]", overridden)
	}
}
