package run

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func gitSpec(dir, stdin string, args ...string) Spec {
	s := Spec{Name: "git", Args: args, Dir: dir, Timeout: time.Minute}
	if stdin != "" {
		s.Stdin = []byte(stdin)
	}
	return s
}

func TestOwnGitState_RefWriteOutsideTrellisIsRefused(t *testing.T) {
	repo := testiso.Repo(t)
	_, err := Light(clock.Real(), gitSpec(repo, "", "update-ref", "refs/heads/x", "HEAD"))
	if !errors.Is(err, ErrOwnGitState) {
		t.Fatalf("update-ref refs/heads/x: err %v, want ErrOwnGitState", err)
	}
	if _, err := Light(clock.Real(), gitSpec(repo, "", "rev-parse", "--verify", "--quiet", "refs/heads/x")); err == nil {
		t.Errorf("refs/heads/x exists: the refused update-ref ran anyway")
	}
	if _, err := Light(clock.Real(), gitSpec(repo, "", "update-ref", "refs/trellis/replay/a", "HEAD")); err != nil {
		t.Fatalf("update-ref refs/trellis/replay/a: %v, want it allowed", err)
	}
	if _, err := Light(clock.Real(), gitSpec(repo, "", "rev-parse", "--verify", "refs/trellis/replay/a")); err != nil {
		t.Errorf("refs/trellis/replay/a was not written: %v", err)
	}
}

func TestOwnGitState_Forms(t *testing.T) {
	cases := []struct {
		name    string
		spec    Spec
		refused bool
	}{
		{"update-ref a branch", gitSpec("", "", "update-ref", "refs/heads/x", "HEAD"), true},
		{"update-ref HEAD", gitSpec("", "", "update-ref", "HEAD", "abc"), true},
		{"update-ref delete a branch", gitSpec("", "", "update-ref", "-d", "refs/heads/x"), true},
		{"update-ref with a reason", gitSpec("", "", "update-ref", "-m", "why", "refs/tags/v1", "HEAD"), true},
		{"update-ref trellis", gitSpec("", "", "update-ref", "--no-deref", "refs/trellis/replay/a", "abc"), false},
		{"update-ref trellis with a reason", gitSpec("", "", "update-ref", "-m", "why", "refs/trellis/a", "abc"), false},
		{"update-ref escaping the prefix", gitSpec("", "", "update-ref", "refs/trellis/../heads/x", "abc"), true},
		{"update-ref after global options", gitSpec("", "", "-C", "/r", "-c", "a.b=c", "update-ref", "refs/heads/x", "abc"), true},
		{"stdin names a branch", gitSpec("", "update refs/trellis/a abc\nupdate refs/heads/x abc\n", "update-ref", "--stdin"), true},
		{"stdin names only trellis refs", gitSpec("", "update refs/trellis/a abc\ncreate refs/trellis/b abc\n", "update-ref", "--stdin"), false},
		{"symbolic-ref sets HEAD", gitSpec("", "", "symbolic-ref", "HEAD", "refs/heads/x"), true},
		{"symbolic-ref reads HEAD", gitSpec("", "", "symbolic-ref", "--short", "HEAD"), false},
		{"symbolic-ref deletes", gitSpec("", "", "symbolic-ref", "--delete", "refs/heads/x"), true},
		{"symbolic-ref sets a trellis ref", gitSpec("", "", "symbolic-ref", "refs/trellis/head", "refs/heads/x"), false},
		{"update-index", gitSpec("", "", "update-index", "--add", "f"), true},
		{"update-index refresh", gitSpec("", "", "--no-pager", "update-index", "--refresh"), true},
		{"write-tree", gitSpec("", "", "write-tree"), true},
		{"status", gitSpec("", "", "status", "-z"), false},
		{"commit-tree", gitSpec("", "", "commit-tree", "abc", "-m", "x"), false},
		{"not git", Spec{Name: "go", Args: []string{"update-ref", "refs/heads/x"}, Timeout: time.Minute}, false},
		{"git by path", Spec{Name: `C:\Program Files\Git\cmd\git.exe`, Args: []string{"update-ref", "refs/heads/x", "a"}, Timeout: time.Minute}, true},
	}
	for _, c := range cases {
		err := ownGitState(c.spec)
		if c.refused != (err != nil) {
			t.Errorf("%s: err %v, refused want %v", c.name, err, c.refused)
		}
		if err != nil && !errors.Is(err, ErrOwnGitState) {
			t.Errorf("%s: err %v does not wrap ErrOwnGitState", c.name, err)
		}
	}
}

func TestOwnGitState_RefusalNamesTheCommand(t *testing.T) {
	err := ownGitState(gitSpec("", "", "update-ref", "refs/heads/x", "HEAD"))
	if err == nil || !strings.Contains(err.Error(), "git update-ref refs/heads/x") {
		t.Errorf("err %v, want it to name `git update-ref refs/heads/x`", err)
	}
}
