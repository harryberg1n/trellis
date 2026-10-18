package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testsupport"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/store"
)

func TestRecorder_SilentOverAllPayloads(t *testing.T) {
	e := newEnv(t)
	gens := generate(t, e.repo, 1000)
	if len(gens) != 1000 {
		t.Fatalf("generated %d payloads, want 1000", len(gens))
	}
	for i, g := range gens {
		e.do(t, "PostToolUse", g.payload)
		if t.Failed() {
			t.Fatalf("payload %d (shape %d) was not silent", i, g.shape)
		}
	}
	// The Bash and session events, each from its recorded shape. Stop comes
	// after the edits it anchors.
	v := vars(e.repo, "", "")
	for _, c := range []struct{ file, event string }{
		{"sessionstart", "SessionStart"},
		{"userpromptsubmit", "UserPromptSubmit"},
		{"bash_pre", "PreToolUse"},
		{"bash_post", "PostToolUse"},
		{"stop", "Stop"},
		{"sessionend", "SessionEnd"},
	} {
		e.do(t, c.event, fill(readShape(t, c.file), v))
	}

	counts := map[string]int{}
	for _, ev := range e.events(t) {
		counts[ev.Kind]++
	}
	want := map[string]int{"edit": 1001, "turn.anchored": 1, "session.usage": 1}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("event kinds %v, want %v (an internal.error would mean a payload was swallowed, not recorded)", counts, want)
	}
}

func TestRecorder_EditWritesBlobAndEvent(t *testing.T) {
	e := newEnv(t)
	file := filepath.Join(e.repo, "hello.txt")
	testsupport.Write(t, file, "hello\n")
	v := vars(e.repo, file, "hello\n")
	v["@SESSION@"] = "sess-1"

	e.do(t, "PostToolUse", fill(readShape(t, "edit_01_full"), v))

	evs := e.events(t)
	if len(evs) != 1 {
		t.Fatalf("%d events %v, want 1", len(evs), kinds(evs))
	}
	ev := evs[0]
	if ev.Kind != "edit" || ev.Lane != "main" || ev.Session != "sess-1" {
		t.Errorf("event = kind %q lane %q session %q, want edit main sess-1", ev.Kind, ev.Lane, ev.Session)
	}
	if ev.At.IsZero() {
		t.Error("event has no time")
	}
	want := map[string]string{"path": "hello.txt", "blob": "ce013625030ba8dba906f756967f9e9ca394464a"}
	if !reflect.DeepEqual(ev.Detail, want) {
		t.Errorf("detail = %v, want %v", ev.Detail, want)
	}
	if got := testsupport.Git(t, e.repo, "hash-object", "hello.txt"); ev.Detail["blob"] != got {
		t.Errorf("blob %s, git hash-object says %s", ev.Detail["blob"], got)
	}
	testsupport.Git(t, e.repo, "cat-file", "-e", ev.Detail["blob"])
}

func TestRecorder_EditHookSpawnsNothing(t *testing.T) {
	e := newEnv(t)
	gens := generate(t, e.repo, 1000)
	spawned := testsupport.CountGitSpawns(t) // real starts of git, not the Runner's count
	for _, g := range gens {
		e.do(t, "PostToolUse", g.payload)
	}
	if n := spawned(); n != 0 {
		t.Errorf("1000 Edit/Write payloads started git %d times, want 0", n)
	}
	if n := e.rec.Count(); n != 0 {
		t.Errorf("1000 Edit/Write payloads spawned %d children %v, want 0", n, e.rec.Kinds())
	}
	if n := len(e.events(t)); n != 1000 {
		t.Errorf("%d events, want 1000", n)
	}
}

func TestRecorder_StopAnchorsTheTurn(t *testing.T) {
	e := newEnv(t)
	a := filepath.Join(e.repo, "a.txt")
	b := filepath.Join(e.repo, "dir", "b.txt")
	testsupport.Write(t, a, "a\n")
	testsupport.Write(t, b, "b\n")
	e.do(t, "PostToolUse", fill(readShape(t, "write_01_create_full"), vars(e.repo, a, "a\n")))
	e.do(t, "PostToolUse", fill(readShape(t, "edit_01_full"), vars(e.repo, b, "b\n")))
	testsupport.Write(t, filepath.Join(e.repo, "c.txt"), "made by bash\n") // what the Bash command did
	e.do(t, "PostToolUse", fill(readShape(t, "bash_post"), vars(e.repo, "", "")))
	e.do(t, "Stop", fill(readShape(t, "stop"), vars(e.repo, "", "")))

	evs := e.events(t)
	if got, want := kinds(evs), []string{"edit", "edit", "edit", "turn.anchored"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds %v, want %v", got, want)
	}
	if !reflect.DeepEqual(evs[2].Detail, map[string]string{"bash": "1"}) {
		t.Errorf("bash edit detail = %v, want only bash=1 (no blob)", evs[2].Detail)
	}
	anchor := evs[3]
	ref := "refs/trellis/replay/main"
	commit := testsupport.Git(t, e.repo, "rev-parse", ref)
	tree := testsupport.Git(t, e.repo, "rev-parse", ref+"^{tree}")
	if anchor.Detail["commit"] != commit || anchor.Detail["tree"] != tree || anchor.Detail["ref"] != ref {
		t.Errorf("turn.anchored detail %v, want commit %s tree %s ref %s", anchor.Detail, commit, tree, ref)
	}
	files := strings.Fields(testsupport.Git(t, e.repo, "ls-tree", "-r", "--name-only", ref))
	if want := []string{"README.md", "a.txt", "c.txt", "dir/b.txt"}; !reflect.DeepEqual(files, want) {
		t.Errorf("anchored tree holds %v, want %v", files, want)
	}
}

func TestRecorder_ErrorNeverBlocks(t *testing.T) {
	editPayload := func(e *env) []byte {
		f := filepath.Join(e.repo, "x.txt")
		testsupport.Write(t, f, "x\n")
		return fill(readShape(t, "edit_01_full"), vars(e.repo, f, "x\n"))
	}
	pendingKinds := func(e *env) []string {
		m, _ := filepath.Glob(filepath.Join(e.fallback, "events.pending", "*.json"))
		out := make([]string, 0, len(m))
		for _, f := range m {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var ev kernel.Event
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatal(err)
			}
			if ev.Detail["error"] == "" || ev.Detail["event"] != "PostToolUse" {
				t.Errorf("fallback event detail %v lacks the error text or the hook event", ev.Detail)
			}
			out = append(out, ev.Kind)
		}
		return out
	}
	record := func(t *testing.T, e *env, payload []byte) {
		t.Helper()
		var out, errb bytes.Buffer
		if code := Run("PostToolUse", bytes.NewReader(payload), &out, &errb, e.deps); code != 0 {
			t.Errorf("exit %d, want 0", code)
		}
		if out.Len() != 0 || errb.Len() != 0 {
			t.Errorf("stdout %q stderr %q, want both empty", out.String(), errb.String())
		}
	}

	t.Run("state root is a file", func(t *testing.T) {
		e := newEnv(t)
		root := filepath.Join(t.TempDir(), "root")
		testsupport.Write(t, root, "not a directory")
		_, err := store.Open(root, e.deps.Clock)
		if err == nil {
			t.Fatal("store.Open on a file succeeded")
		}
		e.deps.Store, e.deps.Err = nil, err
		record(t, e, editPayload(e))
		if got, want := pendingKinds(e), []string{"internal.error"}; !reflect.DeepEqual(got, want) {
			t.Errorf("fallback pending holds %v, want %v", got, want)
		}
	})
	t.Run("state dir replaced after open", func(t *testing.T) {
		e := newEnv(t)
		if err := os.RemoveAll(e.state); err != nil {
			t.Fatal(err)
		}
		testsupport.Write(t, e.state, "not a directory")
		record(t, e, editPayload(e))
		if got, want := pendingKinds(e), []string{"internal.error"}; !reflect.DeepEqual(got, want) {
			t.Errorf("fallback pending holds %v, want %v", got, want)
		}
	})
	t.Run("bad payload with a good store", func(t *testing.T) {
		e := newEnv(t)
		record(t, e, []byte("{not json"))
		evs := e.events(t)
		if len(evs) != 1 || evs[0].Kind != "internal.error" || evs[0].Detail["error"] == "" || evs[0].Detail["event"] != "PostToolUse" {
			t.Errorf("events %+v, want one internal.error naming the event and the error", evs)
		}
		if got := pendingKinds(e); len(got) != 0 {
			t.Errorf("fallback pending holds %v, want none: the store was fine", got)
		}
	})
	t.Run("file gone before the blob", func(t *testing.T) {
		e := newEnv(t)
		p := editPayload(e)
		if err := os.Remove(filepath.Join(e.repo, "x.txt")); err != nil {
			t.Fatal(err)
		}
		record(t, e, p)
		if got, want := kinds(e.events(t)), []string{"internal.error"}; !reflect.DeepEqual(got, want) {
			t.Errorf("kinds %v, want %v", got, want)
		}
	})
}

func TestRecorder_EditIsLazyStopIsDurable(t *testing.T) {
	e := newEnv(t)
	var synced []string
	e.st.SetFsync(func(f *os.File) error {
		synced = append(synced, filepath.Base(f.Name()))
		return f.Sync()
	})
	a := filepath.Join(e.repo, "a.txt")
	testsupport.Write(t, a, "a\n")
	e.do(t, "PostToolUse", fill(readShape(t, "write_01_create_full"), vars(e.repo, a, "a\n")))
	e.do(t, "PostToolUse", fill(readShape(t, "edit_01_full"), vars(e.repo, a, "a\n")))
	e.do(t, "PostToolUse", fill(readShape(t, "bash_post"), vars(e.repo, "", "")))
	if len(synced) != 0 {
		t.Fatalf("3 edits synced %v, want nothing", synced)
	}

	e.do(t, "Stop", fill(readShape(t, "stop"), vars(e.repo, "", "")))
	if want := []string{"events.jsonl", "state.json"}; !reflect.DeepEqual(synced, want) {
		t.Errorf("Stop synced %v, want %v", synced, want)
	}

	synced = nil
	e.do(t, "SessionEnd", fill(readShape(t, "sessionend"), vars(e.repo, "", "")))
	if want := []string{"events.jsonl", "state.json"}; !reflect.DeepEqual(synced, want) {
		t.Errorf("SessionEnd synced %v, want %v", synced, want)
	}
	if got, want := kinds(e.events(t)), []string{"edit", "edit", "edit", "turn.anchored", "session.usage"}; !reflect.DeepEqual(got, want) {
		t.Errorf("kinds %v, want %v", got, want)
	}
}

func fallbackFiles(e *env) []string {
	m, _ := filepath.Glob(filepath.Join(e.fallback, "events.pending", "*"))
	return m
}

func TestRecorder_NonRepoCwdIsSilentNoop(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir() // no .git here or above, inside the sandbox
	f := filepath.Join(dir, "x.txt")
	testsupport.Write(t, f, "x\n")
	for _, c := range []struct{ event, shape string }{
		{"PostToolUse", "edit_01_full"},
		{"PostToolUse", "bash_post"},
		{"Stop", "stop"},
		{"SessionEnd", "sessionend"},
	} {
		e.do(t, c.event, fill(readShape(t, c.shape), vars(dir, f, "x\n")))
	}
	if evs := e.events(t); len(evs) != 0 {
		t.Errorf("events %v, want none", kinds(evs))
	}
	if got := fallbackFiles(e); len(got) != 0 {
		t.Errorf("fallback holds %v, want nothing parked", got)
	}
}

func TestRecorder_FileOutsideRepoIsIgnored(t *testing.T) {
	e := newEnv(t)
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	testsupport.Write(t, outside, "x\n")
	e.do(t, "PostToolUse", fill(readShape(t, "edit_01_full"), vars(e.repo, outside, "x\n")))
	if evs := e.events(t); len(evs) != 0 {
		t.Errorf("events %v, want none", kinds(evs))
	}
	if got := fallbackFiles(e); len(got) != 0 {
		t.Errorf("fallback holds %v, want nothing parked", got)
	}
}

type panicRunner struct{}

func (panicRunner) Light(run.Spec) (run.Result, error) { panic("runner exploded") }

func TestRecorder_DoublePanicStillSilent(t *testing.T) {
	e := newEnv(t)
	// The anchor panics; then the internal.error append panics too, in fsync.
	e.st.SetFsync(func(*os.File) error { panic("disk exploded") })
	e.deps.Runner = panicRunner{}

	var out, errb bytes.Buffer
	code := Run("Stop", bytes.NewReader(fill(readShape(t, "stop"), vars(e.repo, "", ""))), &out, &errb, e.deps)

	if code != 0 || out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("exit %d, stdout %q, stderr %q; want 0, empty, empty", code, out.String(), errb.String())
	}
	if got := fallbackFiles(e); len(got) != 1 {
		t.Errorf("fallback holds %v, want the internal.error parked there", got)
	}
}

func TestRecorder_SpawnShimSeesTheStopAnchor(t *testing.T) {
	e := newEnv(t)
	spawned := testsupport.CountGitSpawns(t)
	e.do(t, "Stop", fill(readShape(t, "stop"), vars(e.repo, "", "")))
	if n := spawned(); n == 0 {
		t.Error("Stop started git 0 times: the shim would not catch an Edit that spawned")
	}
}

func TestRecorder_UnbornHeadIsSilent(t *testing.T) {
	e := newEnv(t)
	repo := filepath.Join(t.TempDir(), "fresh")
	testsupport.Git(t, filepath.Dir(repo), "init", "-q", "-b", "trunk", repo)
	f := filepath.Join(repo, "a.txt")
	testsupport.Write(t, f, "a\n")
	e.do(t, "PostToolUse", fill(readShape(t, "edit_01_full"), vars(repo, f, "a\n")))
	e.do(t, "Stop", fill(readShape(t, "stop"), vars(repo, "", "")))

	evs := e.events(t)
	if got, want := kinds(evs), []string{"edit", "turn.anchored"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds %v, want %v", got, want)
	}
	if evs[0].Lane != "trunk" {
		t.Errorf("lane %q, want trunk", evs[0].Lane)
	}
	if files := testsupport.Git(t, repo, "ls-tree", "-r", "--name-only", "refs/trellis/replay/trunk"); files != "a.txt" {
		t.Errorf("anchored tree holds %q, want a.txt", files)
	}
}

func TestRecorder_LinkedWorktreeRecordsItsBranch(t *testing.T) {
	e := newEnv(t)
	wt := filepath.Join(filepath.Dir(e.repo), "wt")
	testsupport.Git(t, e.repo, "worktree", "add", "-q", "-b", "side/lane", wt)
	f := filepath.Join(wt, "w.txt")
	testsupport.Write(t, f, "w\n")
	e.do(t, "PostToolUse", fill(readShape(t, "edit_01_full"), vars(wt, f, "w\n")))
	e.do(t, "Stop", fill(readShape(t, "stop"), vars(wt, "", "")))

	evs := e.events(t)
	if got, want := kinds(evs), []string{"edit", "turn.anchored"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds %v, want %v", got, want)
	}
	if evs[0].Lane != "side/lane" || evs[0].Detail["path"] != "w.txt" || evs[1].Lane != "side/lane" {
		t.Errorf("events %+v, want lane side/lane and path w.txt", evs)
	}
	if got := testsupport.Git(t, e.repo, "rev-parse", "--verify", "-q", "refs/trellis/replay/side--lane"); got == "" {
		t.Error("no replay ref for side/lane")
	}
}

func TestRecorder_NestedCwdFindsRoot(t *testing.T) {
	e := newEnv(t)
	sub := filepath.Join(e.repo, "pkg", "deep")
	f := filepath.Join(sub, "n.txt")
	testsupport.Write(t, f, "n\n")
	e.do(t, "PostToolUse", fill(readShape(t, "edit_01_full"), vars(sub, f, "n\n")))

	evs := e.events(t)
	if len(evs) != 1 || evs[0].Kind != "edit" || evs[0].Lane != "main" || evs[0].Detail["path"] != "pkg/deep/n.txt" {
		t.Errorf("events %+v, want one edit on main for pkg/deep/n.txt", evs)
	}
}

func TestRecorder_SyncFailureBecomesInternalError(t *testing.T) {
	e := newEnv(t)
	e.st.SetFsync(func(*os.File) error { return errors.New("disk full") })
	e.do(t, "Stop", fill(readShape(t, "stop"), vars(e.repo, "", "")))

	m, _ := filepath.Glob(filepath.Join(e.fallback, "events.pending", "*.json"))
	if len(m) != 1 {
		t.Fatalf("fallback holds %v, want the internal.error", m)
	}
	raw, err := os.ReadFile(m[0])
	if err != nil {
		t.Fatal(err)
	}
	var ev kernel.Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Kind != "internal.error" || !strings.Contains(ev.Detail["error"], "syncing after turn.anchored") || !strings.Contains(ev.Detail["error"], "disk full") {
		t.Errorf("fallback event %+v, want an internal.error naming the failed sync", ev)
	}
}

func TestRecorder_ObjectToolInputIsSilent(t *testing.T) {
	e := newEnv(t)
	body := `{"session_id":"s","cwd":` + strconv.Quote(e.repo) + `,"hook_event_name":"%s","tool_name":"mcp__db__query",` +
		`"tool_input":{"command":{"sql":"select 1","args":[1,2]},"file_path":["a","b"]},"tool_response":{"rows":[]}}`
	for _, event := range []string{"PreToolUse", "PostToolUse"} {
		e.do(t, event, []byte(strings.Replace(body, "%s", event, 1)))
	}
	if evs := e.events(t); len(evs) != 0 {
		t.Errorf("events %v, want none: not our tool, and not an error either", kinds(evs))
	}
}

func TestRecorder_UsesTheRepoTheCallerLocated(t *testing.T) {
	e := newEnv(t)
	loc, err := Locate(e.repo)
	if err != nil {
		t.Fatal(err)
	}
	e.deps.Repo = &loc
	f := filepath.Join(e.repo, "k.txt")
	testsupport.Write(t, f, "k\n")
	// The payload's cwd is nowhere near a repo; only the caller's Repo can place the edit.
	e.do(t, "PostToolUse", fill(readShape(t, "edit_01_full"), vars(t.TempDir(), f, "k\n")))
	if evs := e.events(t); len(evs) != 1 || evs[0].Kind != "edit" || evs[0].Detail["path"] != "k.txt" {
		t.Errorf("events %+v, want one edit of k.txt", evs)
	}
}
