package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/clock"
	runner "github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
	"github.com/harryberg1n/trellis/internal/tree"
)

func TestTree_NoVerbPrintsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"tree"}, &out, &errb); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	for _, want := range []string{"usage: trellis tree <verb>", "new <name>", "sync", "merge [<name>] [--check CMD] [--rebase]", "rm [<name>]"} {
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
	testiso.Git(t, repo, "fetch", "-q", "origin")
	testiso.Git(t, repo, "remote", "set-head", "origin", "main")
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
	if got := string(log); !strings.Contains(got, `"kind":"tree.opened"`) || !strings.Contains(got, `"tree":"feat"`) {
		t.Errorf("events.jsonl = %q, want a tree opened for feat", got)
	}
}

func TestHelp_EveryVerbPrintsUsageInOneVoice(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"install", "--help"}, "usage: trellis install [--config-dir D] [--repo R] [--dry] [--allow-dev]\n"},
		{[]string{"doctor", "--help"}, "usage: trellis doctor [--config-dir D] [--repo R]\n"},
		{[]string{"eject", "--help"}, "usage: trellis eject [--config-dir D] [--repo R]\n"},
		{[]string{"report", "--help"}, "usage: trellis report [--since DATE] [--issue]\n"},
		{[]string{"allow", "--help"}, "usage: trellis allow <row-id>\n"},
		{[]string{"tree", "new", "--help"}, "usage: trellis tree new <name> [--path P]\n"},
		{[]string{"tree", "sync", "--help"}, "usage: trellis tree sync\n"},
		{[]string{"tree", "merge", "--help"}, "usage: trellis tree merge [<name>] [--check CMD] [--rebase]\n"},
		{[]string{"tree", "rm", "--help"}, "usage: trellis tree rm [<name>] [--force]\n"},
		{[]string{"tree", "drop", "--help"}, "usage: trellis tree drop [<name>] --reason R\n"},
		{[]string{"tree", "adopt", "--help"}, "usage: trellis tree adopt <branch> [--path P]\n"},
		{[]string{"tree", "list", "--help"}, "usage: trellis tree list [--size]\n"},
		{[]string{"tree", "prune", "--help"}, "usage: trellis tree prune [--dry] [--backup]\n"},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		code := run(c.args, &out, &errb)
		got := out.String() + errb.String()
		if code != 0 || !strings.HasPrefix(got, c.want) || strings.Contains(got, "Usage of") {
			t.Errorf("trellis %v: exit %d, output %q; want exit 0 and it to start with %q", c.args, code, got, c.want)
		}
	}
}

func TestTree_MainUsageNamesTheFourVerbs(t *testing.T) {
	var out, errb bytes.Buffer
	run([]string{"help"}, &out, &errb)
	if !strings.Contains(out.String(), "trellis tree new|adopt|sync|merge|rm|drop|list|prune") {
		t.Errorf("main usage lacks the tree line:\n%s", out.String())
	}
}

// Issue #53: agents pushed and opened the PR themselves, and ran the check as
// a step of its own, so the merge was not bound to the head the gate judged.
func TestTree_UsageShowsTheLoopWithMergeAsTheOnlyStepAfterTheCommit(t *testing.T) {
	var out, errb bytes.Buffer
	treeCmd([]string{"--help"}, treeEnv{}, &out, &errb)
	for _, line := range []string{
		"  trellis tree new fix/login                          # from the primary checkout\n",
		"  git -C ../repo--fix--login commit -m 'fix: ...'    # work and commit in the tree it printed\n",
		"  trellis tree merge fix/login --check 'make check'  # from the primary: check, push, PR, merge, clean up\n",
	} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("tree usage lacks %q:\n%s", line, out.String())
		}
	}
}

func TestTree_FixableFailuresExitTwoOthersOne(t *testing.T) {
	if got := treeExit(&tree.Fixable{Msg: "x"}); got != 2 {
		t.Errorf("fixable exit = %d, want 2", got)
	}
	if got := treeExit(fmt.Errorf("wrapped: %w", &tree.Fixable{Msg: "x"})); got != 2 {
		t.Errorf("wrapped fixable exit = %d, want 2", got)
	}
	if got := treeExit(errors.New("plain")); got != 1 {
		t.Errorf("plain exit = %d, want 1", got)
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

func TestTree_DropWithoutAReasonExitsTwo(t *testing.T) {
	var out, errb bytes.Buffer
	code := treeCmd([]string{"drop", "feat"}, treeEnv{Dir: testiso.Repo(t), Runner: runner.Exec{Clock: clock.Real()}}, &out, &errb)
	if code != 2 || errb.String() != "trellis tree drop: --reason is needed: say why the work is dropped\n" {
		t.Errorf("exit %d, stderr %q; want 2 and the reason line", code, errb.String())
	}
}
