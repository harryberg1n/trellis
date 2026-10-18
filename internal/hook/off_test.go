package hook

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

func wdOf(dir string) func() (string, error) { return func() (string, error) { return dir, nil } }

func TestOff_AnswersFromTheCwdAndTheMarkerAtTheRepoRoot(t *testing.T) {
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
		name string
		cwd  string
		off  string
		want bool
	}{
		{"repo with the marker", on, "", false},
		{"a subdirectory of it", sub, "", false},
		{"a linked worktree carrying the marker", tree, "", false},
		{"repo without the marker", bare, "", true},
		{"TRELLIS_OFF=1 in a repo with the marker", on, "1", true},
		{"outside any repo", outside, "", true},
		{"no cwd at all", "", "", true},
		{"relative cwd", "a/b", "", true},
	}
	for _, c := range cases {
		if got := Off(c.cwd, c.off); got != c.want {
			t.Errorf("%s: Off = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCwd_IsThePayloadsElseTheProcessesElseNothing(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		wd   func() (string, error)
		want string
	}{
		{"payload cwd wins", `{"cwd":"/p"}`, wdOf("/w"), "/p"},
		{"no cwd in the payload", `{"hook_event_name":"Stop"}`, wdOf("/w"), "/w"},
		{"garbage", "not json", wdOf("/w"), "/w"},
		{"nothing known", "", func() (string, error) { return "", errors.New("gone") }, ""},
	}
	for _, c := range cases {
		if got := Cwd([]byte(c.raw), c.wd); got != c.want {
			t.Errorf("%s: Cwd = %q, want %q", c.name, got, c.want)
		}
	}
}
