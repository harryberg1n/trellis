// Copied from aphrollo-tools internal/testiso/testiso.go at 12b3724e.
// Incident #1043: tests wrote into the real repository and the global git config.

// Package testiso is the one place a test binary is cut off from the git state
// of the box it runs on: the repositories around it, the environment a git hook
// or a gate spawn handed it, and the operator's own git config. Every
// package's TestMain calls Main (or Isolate, when it has setup of its own), so
// a fixture that runs a bare `git init`, `git config` or `git commit` can only
// ever reach a repository the test made.
package testiso

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// staleRoot is how old a root directory of a finished-or-killed run must be
// Main is the whole TestMain of a package: it isolates the run, runs m, removes
// what the isolation made and exits with m's code. Every package with tests
// calls it, as `func TestMain(m *testing.M) { testiso.Main(m) }`.
func Main(m *testing.M) {
	os.Exit(runIsolated(m.Run))
}

// runIsolated is Main without the exit, for the tests of Main itself.
func runIsolated(run func() int) int {
	root, err := MkRoot("trellis-ti-")
	if err != nil {
		panic(err)
	}
	if _, err := Isolate(root); err != nil {
		panic(err)
	}
	code := run()
	RemoveAll(root)
	return code
}

var (
	// realHome is the home directory the process started with, kept so a test
	// can look at the operator's real files from outside the sandbox.
	realHome string
	// sandboxRoot is the root directory Isolate made the run's world under.
	sandboxRoot string
	template    struct {
		once sync.Once
		dir  string
		err  error
	}
)

// Repo returns the root of a fresh git repository on main with one commit,
// copied from a template built once per test binary. It lives under the test's
// own temp dir, inside the sandbox, and is checked by Verify.
func Repo(t testing.TB) string {
	t.Helper()
	if sandboxRoot == "" {
		t.Fatal("testiso.Repo: the package's TestMain did not call testiso.Main")
	}
	template.once.Do(func() {
		template.dir = filepath.Join(sandboxRoot, "template")
		template.err = BuildRepo(template.dir, map[string]string{"README.md": "template\n"})
	})
	if template.err != nil {
		t.Fatalf("testiso.Repo: building the template: %v", template.err)
	}
	dst := filepath.Join(t.TempDir(), "repo")
	if err := CopyRepo(dst, template.dir); err != nil {
		t.Fatalf("testiso.Repo: copying the template: %v", err)
	}
	if err := Verify(dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

// Verify refuses a root a test may not write to: anything outside the
// directory Main made for the run. A root that is itself inside a git checkout
// is named as the real repository.
func Verify(root string) error {
	if sandboxRoot == "" {
		return errors.New("testiso: Main has not isolated this run")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	abs = resolve(abs)
	if inside(resolve(sandboxRoot), abs) {
		return nil
	}
	if repo := enclosingRepo(abs); repo != "" {
		return fmt.Errorf("testiso: %s is outside the sandbox %s, inside the real repository %s", abs, sandboxRoot, repo)
	}
	return fmt.Errorf("testiso: %s is outside the sandbox %s", abs, sandboxRoot)
}

// inside reports whether path is dir or under it.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// before the next run of its family removes it. No test binary of this module
// runs for anything near it.
const staleRoot = 2 * time.Hour

// MkRoot makes the root directory of a test binary's run under the temp dir,
// named with prefix. A binary killed by a timeout or a signal never reaches
// the removal at the end of its TestMain, so before making its own root it
// removes the directories of the same family older than staleRoot: the temp
// dir, RAM-backed on some boxes, does not fill up one killed run at a time.
func MkRoot(prefix string) (string, error) {
	tmp := os.TempDir()
	if entries, err := os.ReadDir(tmp); err == nil {
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().Before(time.Now().Add(-staleRoot)) {
				RemoveAll(filepath.Join(tmp, e.Name()))
			}
		}
	}
	return os.MkdirTemp(tmp, prefix)
}

// RemoveAll removes dir and everything under it, making read-only
// directories writable first: a Go module cache is read-only, and so is a
// directory a test took away its own write bit on, and os.RemoveAll leaves
// either behind.
func RemoveAll(dir string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if d != nil && d.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(dir)
}

// homeLayout maps every variable a Go program, git, or a tool a test spawns
// resolves the operator's home, config, data or cache directory from to its
// directory under fake.
func homeLayout(fake string) map[string]string {
	return map[string]string{
		"HOME":            fake,
		"USERPROFILE":     fake,
		"APPDATA":         filepath.Join(fake, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(fake, "AppData", "Local"),
		"XDG_CONFIG_HOME": filepath.Join(fake, ".config"),
		"XDG_DATA_HOME":   filepath.Join(fake, ".local", "share"),
		"XDG_CACHE_HOME":  filepath.Join(fake, ".local", "cache"),
		"XDG_STATE_HOME":  filepath.Join(fake, ".local", "state"),
	}
}

// Isolate cuts the rest of this process's life off from the box's git world
// and answers the fake home it made under root, which the caller owns and
// removes. It
//   - drops every GIT_* variable the process inherited, so a hook's GIT_DIR,
//     GIT_INDEX_FILE or GIT_WORK_TREE can not redirect a fixture's git at the
//     repository the hook runs for;
//   - moves TMPDIR, TMP, TEMP and GOTMPDIR (which t.TempDir reads first) under root, and sets GIT_CEILING_DIRECTORIES
//     to root and to the root of the repository the process was started in,
//     so no directory a test makes, and no package directory it runs from, can
//     discover a real repository by walking up;
//   - points HOME and every spelling of it, GIT_CONFIG_GLOBAL and the XDG
//     directories into the fake home, and switches the system config off, so
//     `git config --global` writes a file the test owns, one that already
//     tells git to take Windows paths past 260 characters, which nested temp
//     roots reach;
//   - sends CLAUDE_CONFIG_DIR, where the gate keeps its state, under root, and
//     points core.hooksPath at an empty dir so no repo hook a test reaches runs;
//   - switches git's post-commit auto maintenance off.
//   - sets GOWORK=off, so no go.work of the box, the runner or a directory above
//     the temp root is in view of a `go` a test spawns in a throwaway module.
//
// The Go toolchain's cache locations are pinned where they resolve now, so
// moving the home does not make every `go` a test spawns rebuild the world.
func Isolate(root string) (home string, err error) {
	if realHome == "" {
		realHome, _ = os.UserHomeDir()
	}
	// Before the temp directory moves: the first MSYS process of the box fixes
	// what /tmp is for every shell after it, and it must not be one of ours.
	anchor()
	pinToolchainHomes()
	tmp := filepath.Join(root, "tmp")
	home = filepath.Join(root, "home")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", err
	}
	env := map[string]string{"TMPDIR": tmp, "TMP": tmp, "TEMP": tmp, "GOTMPDIR": tmp, "GOWORK": "off"}
	for name, path := range homeLayout(home) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", err
		}
		env[name] = path
	}
	gitconfig := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[core]\n\tlongpaths = true\n"), 0o644); err != nil {
		return "", err
	}
	cwd, _ := os.Getwd()
	env["GIT_CEILING_DIRECTORIES"] = CeilingList(root, enclosingRepo(cwd))
	env["GIT_CONFIG_GLOBAL"] = gitconfig
	env["GIT_CONFIG_NOSYSTEM"] = "1"
	// The gate's state dir is found through CLAUDE_CONFIG_DIR before the home.
	env["CLAUDE_CONFIG_DIR"] = filepath.Join(root, "claude")
	// The environment changes only once everything it names exists: a root that
	// cannot hold them leaves the process as it was.
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "GIT_") {
			_ = os.Unsetenv(name) // a name taken from the environment is always valid
		}
	}
	for name, value := range env {
		_ = os.Setenv(name, value) // fails only on an empty or malformed name, and these are literals
	}
	DisableMaintenanceAndHooks(filepath.Join(root, "nohooks"), func(k, v string) { _ = os.Setenv(k, v) })
	_ = os.Setenv("PATH", withoutQueueShims(os.Getenv("PATH")))
	sandboxRoot = root
	return home, nil
}

// queueShimDir is the name of the directory the box's queue shims (git, cargo)
// live in.
const queueShimDir = "cargo-queue"

// withoutQueueShims is a PATH list without the queue shim directories, order
// kept. A fixture's git is git itself: the shim costs a process of its own on
// every spawn, and the queue it guards serves a session's work, never a
// throwaway repo. A box with no shim, and CI, has nothing to drop.
func withoutQueueShims(path string) string {
	var keep []string
	for _, dir := range filepath.SplitList(path) {
		if filepath.Base(filepath.Clean(dir)) != queueShimDir {
			keep = append(keep, dir)
		}
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

// MustIsolate is Isolate for a TestMain that has setup of its own: it panics
// when the isolation cannot be made, since no test may run unisolated.
func MustIsolate(root string) (home string) {
	home, err := Isolate(root)
	if err != nil {
		panic(err)
	}
	return home
}

// enclosingRepo is the nearest directory at or above dir holding a `.git`,
// file or directory, or "" when there is none: the checkout a test binary is
// started inside, which git would otherwise find from the package directory.
func enclosingRepo(dir string) string {
	// walk-terminates: dir becomes its parent each turn, and the walk returns at the root
	for dir != "" {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// pinToolchainHomes writes the Go toolchain's resolved cache and env-file
// locations, and the rustup home, into the environment where they default
// under the home dir being replaced. A toolchain that cannot answer leaves the
// variables as they were.
func pinToolchainHomes() {
	// exec-ok: testiso cannot import internal/run: run's own tests run under testiso.Main, so the import would be a cycle in test.
	out, _ := exec.Command("go", "env", "-json", "GOPATH", "GOCACHE", "GOMODCACHE", "GOENV").Output() // stderr-ok: a failed lookup leaves the variables unpinned, and go says nothing a caller could use
	var resolved map[string]string
	_ = json.Unmarshal(out, &resolved)
	for name, value := range resolved {
		_ = os.Setenv(name, value)
	}
	home, _ := os.UserHomeDir()
	_ = os.Setenv("RUSTUP_HOME", cmp.Or(os.Getenv("RUSTUP_HOME"), filepath.Join(home, ".rustup")))
}
