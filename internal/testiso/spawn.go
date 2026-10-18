package testiso

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// CountGitSpawns puts a git shim first on PATH for the test. Each time
// anything starts git by name, the shim records a line and then runs the real
// git. The returned func is how many times that has happened. It counts real
// spawns, whatever code path made them, so a test of "this starts no process"
// can fail.
func CountGitSpawns(t *testing.T) (count func() int) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "spawns")
	if err := os.WriteFile(counter, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, shimName), []byte(shimScript(gitPath, counter)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		raw, err := os.ReadFile(counter)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(raw), "x")
	}
}
