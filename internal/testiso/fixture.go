package testiso

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The helpers a fixture needs and only os, exec and testing provide live here
// rather than in testsupport: testsupport imports run, run's tests use
// testiso.Main, and the import would be a cycle in test.

// The author of every commit a test makes: BuildRepo sets it in the repo
// config, so a fixture never passes -c user.* itself.
const (
	AuthorName  = "t"
	AuthorEmail = "t@example.invalid"
)

// Git runs git in dir and returns its trimmed stdout; it fails the test on a
// non-zero exit.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	// exec-ok: testiso cannot import internal/run: run's own tests run under testiso.Main, so the import would be a cycle in test.
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

// CommitAll commits every tracked change in repo, as the repo's own identity.
func CommitAll(t testing.TB, repo, msg string) {
	t.Helper()
	Git(t, repo, "commit", "-qam", msg)
}

// Write writes content to path, making its directories.
func Write(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// FakeTool puts a program called name first on PATH for the test. It writes
// stderr to its standard error and exits with code. Use it for a tool the test
// machine may not have (gitleaks) or must not run for real.
func FakeTool(t testing.TB, name string, code int, stderr string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name+toolExt), []byte(toolScript(code, stderr)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// PathWithOnly sets PATH for the test to the directories holding the named
// tools and nothing else, so a tool the machine may have (gitleaks) is not
// found. With no tools PATH is an empty directory: nothing starts.
func PathWithOnly(t testing.TB, tools ...string) {
	t.Helper()
	var dirs []string
	for _, tool := range tools {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, filepath.Dir(p))
	}
	if len(dirs) == 0 {
		dirs = append(dirs, t.TempDir())
	}
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

// AddTree adds a linked worktree of the repo at primary, on a new branch, in a
// directory called dir, and returns its path. The tree lives under a short
// directory of the sandbox, not beside the primary: the primary sits under the
// test's temp dir, named for the whole test name, and Git for Windows refuses a
// worktree path past 215 characters. The tree is removed when the test ends.
//
// Adapted from aphrollo-tools internal/tdd/core/gitpathlimit_windows.go at
// 12b3724e. Incident: a fixture worktree under a long test name failed with
// "fatal: '$GIT_DIR' too big".
func AddTree(t testing.TB, primary, dir, branch string) string {
	t.Helper()
	if sandboxRoot == "" {
		t.Fatal("testiso.AddTree: the package's TestMain did not call testiso.Main")
	}
	parent := filepath.Join(sandboxRoot, "wt")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	slot, err := os.MkdirTemp(parent, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(slot) })
	tree := filepath.Join(canonical(slot), dir)
	if err := treePathError(tree, gitPathLimit()); err != nil {
		t.Fatal(err)
	}
	Git(t, primary, "worktree", "add", "-q", "-b", branch, tree)
	return tree
}

// treePathError is the error for a worktree path git for Windows will refuse:
// longer than limit, where limit is 0 when git has no limit.
func treePathError(tree string, limit int) error {
	if limit > 0 && len(tree) > limit {
		return fmt.Errorf("testiso.AddTree: the worktree path is %d characters; git for Windows refuses a worktree path over %d", len(tree), limit)
	}
	return nil
}

// ForeignSettings is a settings file with two foreign hook entries, odd
// spacing, a numeric 1.0 and keys in an order no encoder would produce: what
// install must leave byte for byte.
const ForeignSettings = `{ "zeta":{"b":1,"a":2},
  "model" : "opus",
  "ratio": 1.0,
  "hooks" : {
    "Stop":[ {"hooks":[{"type":"command","command":"foreign-stop"}]} ],
	"PreToolUse" : [{"matcher":"Bash","hooks":[{"type":"command","command":"foreign-pre","timeout":5}]}]
  },
  "alpha": [3, 2.50, 1]
}
`
