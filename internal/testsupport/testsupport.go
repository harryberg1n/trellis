// Package testsupport holds the helpers more than one package's tests use:
// running git in a fixture repo, writing a file, and a spawn-counting runner.
// Production code does not import it.
package testsupport

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/run"
)

// Git runs git in dir and returns its trimmed stdout; it fails the test on a
// non-zero exit.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

// CommitAll commits every tracked change in repo.
func CommitAll(t testing.TB, repo, msg string) {
	t.Helper()
	Git(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-qam", msg)
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

// Recorder is a Runner over the real clock that counts what it spawns.
func Recorder() *run.Recorder {
	return &run.Recorder{Inner: run.Exec{Clock: clock.Real()}}
}
