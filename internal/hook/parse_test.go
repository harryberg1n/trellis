package hook

import (
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testsupport"

	"github.com/harryberg1n/trellis/internal/kernel"
)

func TestParse_EventsAndContext(t *testing.T) {
	repo := newEnv(t).repo
	file := filepath.Join(repo, "internal", "x.go")
	v := vars(repo, file, "package x\n")
	transcript := v["@TRANSCRIPT@"]
	sess := "00000000-0000-4000-8000-000000000001"

	cases := []struct {
		name    string
		shape   string
		event   string
		kind    string
		detail  map[string]string
		tool    string
		args    map[string]string
		nothing bool
	}{
		{
			name: "edit", shape: "edit_01_full", event: "PostToolUse", kind: "edit",
			detail: map[string]string{"path": "internal/x.go"}, tool: "Edit",
			args: map[string]string{"file_path": file, "path": "internal/x.go", "cwd": repo, "repo": repo},
		},
		{
			name: "write", shape: "write_01_create_full", event: "PostToolUse", kind: "edit",
			detail: map[string]string{"path": "internal/x.go"}, tool: "Write",
			args: map[string]string{"file_path": file, "path": "internal/x.go", "cwd": repo, "repo": repo},
		},
		{
			name: "bash after", shape: "bash_post", event: "PostToolUse", kind: "edit",
			detail: map[string]string{"bash": "1"}, tool: "Bash",
			args: map[string]string{"command": "go test ./...", "cwd": repo, "repo": repo},
		},
		{
			name: "stop", shape: "stop", event: "Stop", kind: "turn.anchored",
			tool: "", args: map[string]string{"cwd": repo, "repo": repo},
		},
		{
			name: "session end", shape: "sessionend", event: "SessionEnd", kind: "session.usage",
			detail: map[string]string{"transcript": transcript, "reason": "other"},
			args:   map[string]string{"cwd": repo, "repo": repo},
		},
		{
			name: "bash before is context only", shape: "bash_pre", event: "PreToolUse", nothing: true, tool: "Bash",
			args: map[string]string{"command": "ls", "cwd": repo, "repo": repo},
		},
		{name: "session start", shape: "sessionstart", event: "SessionStart", nothing: true, args: map[string]string{"cwd": repo, "repo": repo}},
		{name: "prompt", shape: "userpromptsubmit", event: "UserPromptSubmit", nothing: true, args: map[string]string{"cwd": repo, "repo": repo}},
		{name: "event spelled with dashes", shape: "stop", event: "stop", kind: "turn.anchored", args: map[string]string{"cwd": repo, "repo": repo}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev, ctx, err := Parse(c.event, fill(readShape(t, c.shape), v))
			if err != nil {
				t.Fatal(err)
			}
			if ev.Kind != c.kind {
				t.Errorf("kind %q, want %q", ev.Kind, c.kind)
			}
			if c.nothing {
				if ev.Kind != "" {
					t.Errorf("%s records %q, want nothing", c.event, ev.Kind)
				}
			} else if ev.Lane != "main" || ev.Session != sess {
				t.Errorf("lane %q session %q, want main %s", ev.Lane, ev.Session, sess)
			}
			if !reflect.DeepEqual(ev.Detail, c.detail) {
				t.Errorf("detail %v, want %v", ev.Detail, c.detail)
			}
			if want := (kernel.Context{Tool: c.tool, Args: c.args}); !reflect.DeepEqual(ctx, want) {
				t.Errorf("context %+v, want %+v", ctx, want)
			}
		})
	}
}

func TestParse_LaneFromHeadWithoutASpawn(t *testing.T) {
	e := newEnv(t)
	file := filepath.Join(e.repo, "a.txt")
	testsupport.Write(t, file, "a\n")
	payload := fill(readShape(t, "edit_01_full"), vars(e.repo, file, ""))
	laneOf := func(t *testing.T, payload []byte) string {
		t.Helper()
		ev, _, err := Parse("PostToolUse", payload)
		if err != nil {
			t.Fatal(err)
		}
		return ev.Lane
	}

	testsupport.Git(t, e.repo, "checkout", "-q", "-b", "feature/deep/x")
	if got := laneOf(t, payload); got != "feature/deep/x" {
		t.Errorf("branch lane %q, want feature/deep/x", got)
	}

	// cwd below the repo root still finds the repo.
	sub := fill(readShape(t, "edit_01_full"), map[string]string{
		"@CWD@": filepath.Join(e.repo, "no", "such"), "@FILE@": file, "@SESSION@": "s", "@TRANSCRIPT@": "t",
	})
	if got := laneOf(t, sub); got != "feature/deep/x" {
		t.Errorf("lane from a subdirectory cwd %q, want feature/deep/x", got)
	}

	// A linked worktree has its own HEAD in .git/worktrees/<name>.
	wt := filepath.Join(filepath.Dir(e.repo), "wt")
	testsupport.Git(t, e.repo, "worktree", "add", "-q", "-b", "side/lane", wt)
	testsupport.Write(t, filepath.Join(wt, "w.txt"), "w\n")
	wp := fill(readShape(t, "edit_01_full"), vars(wt, filepath.Join(wt, "w.txt"), ""))
	ev, ctx, err := Parse("PostToolUse", wp)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Lane != "side/lane" || ev.Detail["path"] != "w.txt" || ctx.Args["repo"] != wt {
		t.Errorf("worktree: lane %q path %q repo %q, want side/lane w.txt %s", ev.Lane, ev.Detail["path"], ctx.Args["repo"], wt)
	}

	testsupport.Git(t, e.repo, "checkout", "-q", "--detach")
	if want := "detached-" + testsupport.Git(t, e.repo, "rev-parse", "--short=12", "HEAD"); laneOf(t, payload) != want {
		t.Errorf("detached HEAD lane %q, want %q", laneOf(t, payload), want)
	}
}

func TestParse_Refuses(t *testing.T) {
	e := newEnv(t)
	v := vars(e.repo, filepath.Join(e.repo, "a.txt"), "")
	cases := []struct {
		name    string
		event   string
		payload []byte
		want    string
	}{
		{"empty", "PostToolUse", nil, "empty payload"},
		{"not json", "PostToolUse", []byte("{nope"), "payload"},
		{"unknown event", "PreCompactish", fill(readShape(t, "stop"), v), "unknown hook event"},
		{"no session", "Stop", []byte(`{"cwd":"x"}`), "session_id"},
		{"edit without a path", "PostToolUse", []byte(`{"session_id":"s","cwd":"` + jsonEsc(e.repo) + `","tool_name":"Edit","tool_input":{}}`), "file_path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := Parse(c.event, c.payload)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Parse error %v, want one containing %q", err, c.want)
			}
		})
	}
}

func TestParse_NotOursRecordsNothing(t *testing.T) {
	e := newEnv(t)
	cases := map[string]map[string]string{
		"file outside the repo": vars(e.repo, filepath.Join(filepath.Dir(e.repo), "elsewhere.txt"), ""),
		"no repo above cwd":     vars(filepath.Dir(e.repo), filepath.Join(filepath.Dir(e.repo), "a.txt"), ""),
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			ev, _, err := Parse("PostToolUse", fill(readShape(t, "edit_01_full"), v))
			if err != nil || ev.Kind != "" {
				t.Errorf("Parse = kind %q, %v; want no kind and no error", ev.Kind, err)
			}
		})
	}
}

func TestParse_PreToolUseWithoutASessionIsAPerson(t *testing.T) {
	e := newEnv(t)
	body := `{"cwd":` + strconv.Quote(e.repo) + `,"tool_name":"Bash","tool_input":{"command":"git status"}}`
	ev, ctx, err := Parse("PreToolUse", []byte(body))
	if err != nil {
		t.Fatalf("Parse: %v, want a PreToolUse with no session_id accepted (a person at a git hook)", err)
	}
	want := kernel.Context{Tool: "Bash", Args: map[string]string{"command": "git status", "cwd": e.repo, "repo": e.repo, "person": "1"}}
	if ev.Kind != "" || ev.Session != "" || !reflect.DeepEqual(ctx, want) {
		t.Errorf("event %+v, context %+v; want no kind, no session, context %+v", ev, ctx, want)
	}
	if _, _, err := Parse("PostToolUse", []byte(body)); err == nil || !strings.Contains(err.Error(), "session_id") {
		t.Errorf("PostToolUse without a session: %v, want the session_id error", err)
	}
}

func TestParse_PowerShellCommandIsInTheContext(t *testing.T) {
	e := newEnv(t)
	body := `{"session_id":"s","cwd":` + strconv.Quote(e.repo) + `,"tool_name":"PowerShell","tool_input":{"command":"git status"}}`
	_, ctx, err := Parse("PreToolUse", []byte(body))
	if err != nil || ctx.Args["command"] != "git status" || ctx.Tool != "PowerShell" {
		t.Errorf("context %+v, %v; want tool PowerShell with its command", ctx, err)
	}
}
