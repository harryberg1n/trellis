package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/clock"
	runner "github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestTree_NoVerbPrintsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"tree"}, &out, &errb); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	for _, want := range []string{"usage: trellis tree <verb>", "new <name>", "sync", "merge [--check CMD]", "rm [<name>]"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("usage lacks %q:\n%s", want, errb.String())
		}
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestTree_NewPrintsPathAndRecordsOpenedInTheStore(t *testing.T) {
	state := t.TempDir()
	t.Setenv("TRELLIS_STATE", state)
	// other tests in this package leave remote.origin.url in the sandbox's global config
	noGlobal := filepath.Join(t.TempDir(), "gitconfig")
	testiso.Write(t, noGlobal, "")
	t.Setenv("GIT_CONFIG_GLOBAL", noGlobal)
	repo := testiso.Repo(t)
	origin := filepath.Join(filepath.Dir(repo), "origin.git")
	testiso.Git(t, repo, "init", "--bare", "-q", origin)
	testiso.Git(t, repo, "remote", "add", "origin", origin)
	testiso.Git(t, repo, "push", "-q", "origin", "main")
	env := treeEnv{Dir: repo, Runner: runner.Exec{Clock: clock.Real()}}

	var out, errb bytes.Buffer
	// the flag follows the name
	path := filepath.Join(t.TempDir(), "wt")
	if code := treeCmd([]string{"new", "feat", "--path", path}, env, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}

	if got, want := out.String(), path+"\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	var log []byte
	_ = filepath.WalkDir(state, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == "events.jsonl" {
			log, _ = os.ReadFile(p)
		}
		return nil
	})
	if got := string(log); !strings.Contains(got, `"kind":"lane.opened"`) || !strings.Contains(got, `"lane":"feat"`) {
		t.Errorf("events.jsonl = %q, want a tree opened for feat", got)
	}
}

func TestTree_ErrorIsOneLineOnStderrAndExitOne(t *testing.T) {
	repo := testiso.Repo(t)
	var out, errb bytes.Buffer
	code := treeCmd([]string{"merge"}, treeEnv{Dir: repo, Runner: runner.Exec{Clock: clock.Real()}}, &out, &errb)
	if code != 1 || out.Len() != 0 || strings.Count(errb.String(), "\n") != 1 {
		t.Errorf("exit %d, stdout %q, stderr %q; want 1, empty, one line", code, out.String(), errb.String())
	}
}
