package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
	runner "github.com/harryberg1n/trellis/internal/run"
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

func TestRun_VersionAndHelpFlagsAreTheVerbs(t *testing.T) {
	cases := []struct{ arg, want string }{
		{"--version", "trellis dev (unknown)\n"},
		{"--help", usage},
		{"-h", usage},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		if code := run([]string{c.arg}, &out, &errb); code != 0 {
			t.Errorf("%s: exit %d: %s", c.arg, code, errb.String())
			continue
		}
		if out.String() != c.want {
			t.Errorf("%s printed %q, want %q", c.arg, out.String(), c.want)
		}
	}
}

func TestExitCode_AnInterruptedVerbThatFailedExits130(t *testing.T) {
	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		name string
		ctx  context.Context
		code int
		want int
	}{
		{"no signal, ok", live, 0, 0},
		{"no signal, error", live, 1, 1},
		{"no signal, fixable", live, 2, 2},
		{"signal, verb finished anyway", cancelled, 0, 0},
		{"signal, error", cancelled, 1, 130},
		{"signal, fixable", cancelled, 2, 130},
	} {
		if got := exitCode(c.ctx, c.code); got != c.want {
			t.Errorf("%s: exitCode(%d) = %d, want %d", c.name, c.code, got, c.want)
		}
	}
}

func TestTree_ACancelledContextStopsTheVerbOnOneLine(t *testing.T) {
	repo := testiso.Repo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env := treeEnv{Dir: repo, Runner: runner.Exec{Clock: clock.Real()}, Ctx: ctx}

	var out, errb bytes.Buffer
	code := treeCmd([]string{"new", "feat"}, env, &out, &errb)

	if code != 1 || errb.String() != "trellis tree new: interrupted: context canceled\n" {
		t.Errorf("code %d, stderr %q; want 1 and the interrupted line", code, errb.String())
	}
	if exitCode(ctx, code) != 130 {
		t.Errorf("main would exit %d, want 130", exitCode(ctx, code))
	}
}

func TestReadme_NamesEveryVerbOfTheUsage(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(raw)
	var verbs []string
	in := false
	for _, l := range strings.Split(usage, "\n") {
		switch {
		case l == "verbs:":
			in = true
		case in && strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   "):
			verbs = append(verbs, strings.Fields(l)[0])
		}
	}
	if len(verbs) < 10 {
		t.Fatalf("read %d verbs from the usage: %v", len(verbs), verbs)
	}
	for _, v := range verbs {
		if !strings.Contains(readme, "`trellis "+v) {
			t.Errorf("README does not name the verb `trellis %s`", v)
		}
	}
	for _, v := range []string{"new", "sync", "merge", "rm"} {
		if !strings.Contains(readme, "`trellis tree "+v) {
			t.Errorf("README does not name `trellis tree %s`", v)
		}
	}
	for _, want := range []string{"130", "authenticated `gh`", "VERSION bump is a release"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README lacks %q", want)
		}
	}
}

func TestHonoursInterrupt_OnlyTheVerbsThatReadTheContextCatchSignals(t *testing.T) {
	for verb, want := range map[string]bool{
		"tree": true, "update": true, "report": true,
		"hook": false, "allow": false, "install": false, "doctor": false, "eject": false, "version": false, "help": false,
	} {
		if got := honoursInterrupt([]string{verb}); got != want {
			t.Errorf("honoursInterrupt(%s) = %v, want %v", verb, got, want)
		}
	}
	if honoursInterrupt(nil) {
		t.Error("no verb installed a signal handler")
	}
}

// ctxSpy remembers the context of every child it is asked to run.
type ctxSpy struct{ got []context.Context }

func (s *ctxSpy) Light(spec runner.Spec) (runner.Result, error) {
	s.got = append(s.got, spec.Ctx)
	return runner.Result{}, nil
}

func TestWithReportContext_GivesTheReportsChildrenTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spy := &ctxSpy{}

	env := withReportContext(reportEnv{Runner: spy}, ctx)
	_, _ = env.Runner.Light(runner.Spec{Name: "gh"})

	if len(spy.got) != 1 || spy.got[0] != ctx {
		t.Errorf("the report's child got context %v, want the verb's", spy.got)
	}
}

func TestUsage_NamesTheFlagsOfInstallDoctorEjectAndExit130(t *testing.T) {
	for _, want := range []string{
		"trellis install [--config-dir D] [--repo R] [--dry] [--allow-dev]",
		"trellis doctor [--config-dir D] [--repo R]",
		"trellis eject [--config-dir D] [--repo R]",
		"130 interrupted",
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
	if !strings.Contains(treeUsage, "130 interrupted") {
		t.Error("tree usage lacks the interrupted exit code")
	}
}

func TestTreeUsage_NamesEveryRefusalThatExitsTwo(t *testing.T) {
	for _, want := range []string{
		"uncommitted changes (sync)",
		"commits on origin the tree lacks",
		"a head that moved after the checks judged it",
		"still queued after 30 minutes",
	} {
		if !strings.Contains(treeUsage, want) {
			t.Errorf("tree usage lacks %q", want)
		}
	}
}
