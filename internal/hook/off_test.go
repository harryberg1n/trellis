package hook

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

func wdOf(dir string) func() (string, error) { return func() (string, error) { return dir, nil } }

func TestOff_AnswersFromThePayloadCwdAndTheMarkerAtTheRepoRoot(t *testing.T) {
	bare := testiso.Repo(t)
	on := testiso.Repo(t)
	testiso.Write(t, filepath.Join(on, "trellis.toml"), "# on\n")
	sub := filepath.Join(on, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	tree := testiso.AddTree(t, on, "on-tree", "tree/x")
	testiso.Write(t, filepath.Join(tree, "trellis.toml"), "# on\n") // a tree of a repo that committed it has it
	outside := t.TempDir()

	cases := []struct {
		name    string
		payload string
		off     string
		wd      string
		want    bool
	}{
		{"repo with the marker", testiso.JSONString(on), "", outside, false},
		{"a subdirectory of it", testiso.JSONString(sub), "", outside, false},
		{"a linked worktree carrying the marker", testiso.JSONString(tree), "", outside, false},
		{"repo without the marker", testiso.JSONString(bare), "", outside, true},
		{"TRELLIS_OFF=1 in a repo with the marker", testiso.JSONString(on), "1", outside, true},
		{"outside any repo", testiso.JSONString(outside), "", outside, true},
		{"no cwd in the payload, process in a marked repo", `""`, "", on, false},
		{"no cwd in the payload, process outside any repo", `""`, "", outside, true},
		{"relative cwd", testiso.JSONString("a/b"), "", on, true},
	}
	for _, c := range cases {
		raw := []byte(`{"hook_event_name":"Stop","cwd":` + c.payload + `}`)
		if got := Off(raw, c.off, wdOf(c.wd)); got != c.want {
			t.Errorf("%s: Off = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOff_GarbageAndAnUnreadableWorkingDirectoryAreOff(t *testing.T) {
	if !Off([]byte("not json"), "", wdOf("")) {
		t.Errorf("Off on garbage with no working directory = false, want true")
	}
	if !Off(nil, "", func() (string, error) { return "", errors.New("gone") }) {
		t.Errorf("Off with an unreadable working directory = false, want true")
	}
}
