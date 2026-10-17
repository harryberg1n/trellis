package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestMain(m *testing.M) { testiso.Main(m) }

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
