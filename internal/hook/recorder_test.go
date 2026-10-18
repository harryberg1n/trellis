package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
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
	writeFile(t, file, "hello\n")
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
	if got := gitOut(t, e.repo, "hash-object", "hello.txt"); ev.Detail["blob"] != got {
		t.Errorf("blob %s, git hash-object says %s", ev.Detail["blob"], got)
	}
	gitOut(t, e.repo, "cat-file", "-e", ev.Detail["blob"])
}

func TestRecorder_EditHookSpawnsNothing(t *testing.T) {
	e := newEnv(t)
	for _, g := range generate(t, e.repo, 1000) {
		e.do(t, "PostToolUse", g.payload)
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
	writeFile(t, a, "a\n")
	writeFile(t, b, "b\n")
	e.do(t, "PostToolUse", fill(readShape(t, "write_01_create_full"), vars(e.repo, a, "a\n")))
	e.do(t, "PostToolUse", fill(readShape(t, "edit_01_full"), vars(e.repo, b, "b\n")))
	writeFile(t, filepath.Join(e.repo, "c.txt"), "made by bash\n") // what the Bash command did
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
	commit := gitOut(t, e.repo, "rev-parse", ref)
	tree := gitOut(t, e.repo, "rev-parse", ref+"^{tree}")
	if anchor.Detail["commit"] != commit || anchor.Detail["tree"] != tree || anchor.Detail["ref"] != ref {
		t.Errorf("turn.anchored detail %v, want commit %s tree %s ref %s", anchor.Detail, commit, tree, ref)
	}
	files := strings.Fields(gitOut(t, e.repo, "ls-tree", "-r", "--name-only", ref))
	if want := []string{"README.md", "a.txt", "c.txt", "dir/b.txt"}; !reflect.DeepEqual(files, want) {
		t.Errorf("anchored tree holds %v, want %v", files, want)
	}
}

func TestRecorder_ErrorNeverBlocks(t *testing.T) {
	editPayload := func(e *env) []byte {
		f := filepath.Join(e.repo, "x.txt")
		writeFile(t, f, "x\n")
		return fill(readShape(t, "edit_01_full"), vars(e.repo, f, "x\n"))
	}
	pendingKinds := func(e *env) []string {
		m, _ := filepath.Glob(filepath.Join(e.fallback, "events.pending", "*.json"))
		var out []string
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
	run := func(t *testing.T, e *env, payload []byte) {
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
		writeFile(t, root, "not a directory")
		_, err := store.Open(root, e.deps.Clock)
		if err == nil {
			t.Fatal("store.Open on a file succeeded")
		}
		e.deps.Store, e.deps.Err = nil, err
		run(t, e, editPayload(e))
		if got, want := pendingKinds(e), []string{"internal.error"}; !reflect.DeepEqual(got, want) {
			t.Errorf("fallback pending holds %v, want %v", got, want)
		}
	})
	t.Run("state dir replaced after open", func(t *testing.T) {
		e := newEnv(t)
		if err := os.RemoveAll(e.state); err != nil {
			t.Fatal(err)
		}
		writeFile(t, e.state, "not a directory")
		run(t, e, editPayload(e))
		if got, want := pendingKinds(e), []string{"internal.error"}; !reflect.DeepEqual(got, want) {
			t.Errorf("fallback pending holds %v, want %v", got, want)
		}
	})
	t.Run("bad payload with a good store", func(t *testing.T) {
		e := newEnv(t)
		run(t, e, []byte("{not json"))
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
		run(t, e, p)
		if got, want := kinds(e.events(t)), []string{"internal.error"}; !reflect.DeepEqual(got, want) {
			t.Errorf("kinds %v, want %v", got, want)
		}
	})
}
