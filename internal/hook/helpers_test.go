package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/testiso"
	"github.com/harryberg1n/trellis/internal/testsupport"
)

// env is a fresh repo, a state root outside it, and Deps over both.
type env struct {
	repo     string
	state    string
	fallback string
	st       *store.Store
	rec      *run.Recorder
	deps     Deps
}

func newEnv(t testing.TB) *env {
	t.Helper()
	repo := testiso.Repo(t)
	base := t.TempDir()
	e := &env{repo: repo, state: filepath.Join(base, "state"), fallback: filepath.Join(base, "fallback")}
	st, err := store.Open(e.state, clock.Real())
	if err != nil {
		t.Fatal(err)
	}
	e.st = st
	e.rec = testsupport.Recorder()
	e.deps = Deps{Store: st, Runner: e.rec, Clock: clock.Real(), Rows: kernel.Table, Fallback: e.fallback}
	return e
}

// events is every event in the log, oldest first.
func (e *env) events(t testing.TB) []kernel.Event {
	t.Helper()
	evs, err := e.st.Read(1)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

// do runs one payload through Run and fails the test on any output or a
// non-zero exit.
func (e *env) do(t testing.TB, event string, payload []byte) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := Run(event, bytes.NewReader(payload), &out, &errb, e.deps); code != 0 || out.Len() != 0 || errb.Len() != 0 {
		t.Fatalf("Run(%s) = exit %d, stdout %q, stderr %q; want 0, empty, empty", event, code, out.String(), errb.String())
	}
}

func jsonEsc(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b[1 : len(b)-1])
}

// fill expands a payload template: every value is JSON-escaped, so a Windows
// path or a content full of newlines stays valid JSON.
func fill(shape []byte, vars map[string]string) []byte {
	out := string(shape)
	for k, v := range vars {
		out = strings.ReplaceAll(out, k, jsonEsc(v))
	}
	return []byte(out)
}

func readShape(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "payloads", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// editShapes is the 20 recorded Edit and Write payload shapes.
func editShapes(t testing.TB) [][]byte {
	t.Helper()
	var shapes [][]byte
	for _, pat := range []string{"edit_*.json", "write_*.json"} {
		m, err := filepath.Glob(filepath.Join("testdata", "payloads", pat))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range m {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			shapes = append(shapes, b)
		}
	}
	if len(shapes) != 20 {
		t.Fatalf("found %d Edit/Write shapes in testdata/payloads, want 20", len(shapes))
	}
	return shapes
}

func vars(repo, file, content string) map[string]string {
	return map[string]string{
		"@CWD@":        repo,
		"@FILE@":       file,
		"@CONTENT@":    content,
		"@SESSION@":    "00000000-0000-4000-8000-000000000001",
		"@TRANSCRIPT@": filepath.Join(filepath.Dir(repo), "transcript.jsonl"),
	}
}

// generated is one payload made from a recorded shape.
type generated struct {
	shape   int
	file    string // absolute
	payload []byte
}

func genDir(i int) string {
	dirs := [...]string{"internal/a", "internal/b", "cmd/x", "docs", "pkg/deep/er", ".", "tests"}
	return dirs[i%len(dirs)]
}

// generate makes n PostToolUse Edit/Write payloads from the 20 recorded
// shapes, deterministically: shape i%20, directory i%7, session i%13, content
// size (i*37)%3000+1 bytes. Each file is written under repo, as the harness
// would have left it.
func generate(t testing.TB, repo string, n int) []generated {
	t.Helper()
	shapes := editShapes(t)
	out := make([]generated, n)
	for i := range out {
		file := filepath.Join(repo, filepath.FromSlash(genDir(i)), fmt.Sprintf("f%04d.go", i))
		content := text(i, (i*37)%3000+1)
		testsupport.Write(t, file, content)
		v := vars(repo, file, content)
		v["@SESSION@"] = fmt.Sprintf("00000000-0000-4000-8000-%012d", i%13)
		out[i] = generated{shape: i % 20, file: file, payload: fill(shapes[i%20], v)}
	}
	return out
}

// text is size bytes of lowercase letters and newlines, the same for the same seed.
func text(seed, size int) string {
	b := make([]byte, size)
	x := uint32(seed)*2654435761 + 1
	for i := range b {
		x = x*1664525 + 1013904223
		c := byte(x >> 24)
		if c%41 == 0 {
			b[i] = '\n'
		} else {
			b[i] = 'a' + c%26
		}
	}
	return string(b)
}

func kinds(evs []kernel.Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}
