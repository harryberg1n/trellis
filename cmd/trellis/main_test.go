package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestMain(m *testing.M) { testiso.Main(m) }

// binOption changes how runBin starts the binary.
type binOption func(*exec.Cmd)

// inDir runs the binary with dir as its working directory.
func inDir(dir string) binOption { return func(c *exec.Cmd) { c.Dir = dir } }

// withEnv adds NAME=value entries to the binary's environment.
func withEnv(kv ...string) binOption { return func(c *exec.Cmd) { c.Env = append(c.Env, kv...) } }

// runBin runs the built binary with TRELLIS_STATE=state, stdin on its standard
// input, and returns its exit code, stdout and stderr.
func runBin(t *testing.T, bin, state, stdin string, args []string, opts ...binOption) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "TRELLIS_STATE="+state)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	for _, o := range opts {
		o(cmd)
	}
	var exit *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exit) {
		t.Fatalf("%s %v: %v", bin, args, err)
	}
	return cmd.ProcessState.ExitCode(), out.String(), errb.String()
}

// helloEdit writes hello.txt in repo and returns the PostToolUse payload of an
// Edit of it by session s-1.
func helloEdit(t *testing.T, repo string) []byte {
	t.Helper()
	file := filepath.Join(repo, "hello.txt")
	testiso.Write(t, file, "hello\n")
	return testiso.HookPayload(t, kernel.HookPostToolUse, kernel.ToolEdit, repo,
		map[string]string{"file_path": file, "old_string": "a", "new_string": "b"}, testiso.WithSession("s-1"))
}

// build compiles the trellis command with the given extra go build flags.
func build(t *testing.T, flags ...string) string {
	t.Helper()
	goexe, err := exec.Command("go", "env", "GOEXE").Output()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "trellis"+strings.TrimSpace(string(goexe)))
	args := append([]string{"build", "-o", bin}, flags...)
	args = append(args, ".")
	if out, err := exec.Command("go", args...).CombinedOutput(); err != nil {
		t.Fatalf("go %v: %v\n%s", args, err, out)
	}
	return bin
}

func TestVersion_PrintsBuildInfo(t *testing.T) {
	const pkg = "github.com/harryberg1n/trellis/internal/buildinfo"
	bin := build(t, "-ldflags", "-X "+pkg+".Version=v0.0.1 -X "+pkg+".Commit=abc1234")

	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "trellis v0.0.1 (abc1234)\n"; got != want {
		t.Errorf("trellis version printed %q, want %q", got, want)
	}
}

func TestRun_UnbuiltVersionSaysDev(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if got, want := out.String(), "trellis dev (unknown)\n"; got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}

func TestRun_HelpListsTheVerbs(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"usage: trellis <verb>", "version", "help"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, out.String())
		}
	}
}

func TestRun_NoVerbAndUnknownVerbExitTwo(t *testing.T) {
	for _, args := range [][]string{nil, {"frobnicate"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("run(%v) = %d, want 2", args, code)
		}
		if out.Len() != 0 || !strings.Contains(errb.String(), "usage: trellis <verb>") {
			t.Errorf("run(%v): stdout %q, stderr %q; want usage on stderr only", args, out.String(), errb.String())
		}
	}
}
