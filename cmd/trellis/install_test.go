package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
	"github.com/harryberg1n/trellis/internal/testsupport"
)

const keptSettings = `{ "model" : "opus",
  "hooks" : { "Stop":[ {"hooks":[{"type":"command","command":"foreign"}]} ] }
}
`

// installFixture is a repo with foreign settings, a state root and a stand-in
// binary, with TRELLIS_STATE pointing at the root.
func installFixture(t *testing.T) (env installEnv, repo, state string) {
	t.Helper()
	repo = testiso.Repo(t)
	dir := t.TempDir()
	state = filepath.Join(dir, "state")
	t.Setenv("TRELLIS_STATE", state)
	exe := filepath.Join(dir, "trellis")
	testsupport.Write(t, exe, "stand-in")
	testsupport.Write(t, filepath.Join(repo, ".claude", "settings.local.json"), keptSettings)
	return installEnv{Cwd: repo, Exe: exe}, repo, state
}

func runInstall(env installEnv, args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = runInstallVerb(args, &out, &errb, env)
	return code, out.String(), errb.String()
}

func TestInstallVerbs_InstallDoctorEjectCycle(t *testing.T) {
	env, repo, state := installFixture(t)
	settings := filepath.Join(repo, ".claude", "settings.local.json")

	code, out, errs := runInstall(env, "install", "--dry")
	if code != 0 || errs != "" || !strings.HasPrefix(out, "would copy binary "+filepath.Join(state, "bin", "dev", "trellis")+"\nwould add hook PreToolUse\n") {
		t.Fatalf("dry install: %d %q %q", code, out, errs)
	}
	if code, out, _ := runInstall(env, "doctor"); code != 1 || out != "trellis is not installed: "+settings+"\n" {
		t.Errorf("doctor before install: %d %q", code, out)
	}

	code, out, errs = runInstall(env, "install")
	if code != 0 || errs != "" || !strings.HasPrefix(out, "copy binary ") || !strings.Contains(out, "add hook UserPromptSubmit\n") {
		t.Fatalf("install: %d %q %q", code, out, errs)
	}
	if code, out, errs := runInstall(env, "doctor"); code != 0 || out != "doctor: ok\n" || errs != "" {
		t.Errorf("doctor after install: %d %q %q", code, out, errs)
	}

	code, out, _ = runInstall(env, "eject")
	if code != 0 || !strings.HasPrefix(out, "remove hook Stop\n") {
		t.Fatalf("eject: %d %q", code, out)
	}
	if b, _ := os.ReadFile(settings); string(b) != keptSettings {
		t.Errorf("settings after eject:\n%s", b)
	}
	if code, out, _ := runInstall(env, "eject"); code != 0 || out != "eject: nothing to remove\n" {
		t.Errorf("second eject: %d %q", code, out)
	}
}

func TestInstallVerbs_RepoFlagAndErrors(t *testing.T) {
	env, repo, _ := installFixture(t)
	elsewhere := installEnv{Cwd: t.TempDir(), Exe: env.Exe}
	if code, _, errs := runInstall(elsewhere, "doctor"); code != 1 || !strings.Contains(errs, "not in a git repository") {
		t.Errorf("outside a repo: %d %q", code, errs)
	}
	if code, out, _ := runInstall(elsewhere, "install", "--repo", repo, "--dry"); code != 0 || !strings.Contains(out, "would add hook Stop") {
		t.Errorf("--repo: %d %q", code, out)
	}
	if code, _, errs := runInstall(env, "eject", "--dry"); code != 2 || !strings.Contains(errs, "--dry is an install flag") {
		t.Errorf("eject --dry: %d %q", code, errs)
	}
	if code, _, _ := runInstall(env, "install", "--bogus"); code != 2 {
		t.Errorf("bad flag exits %d, want 2", code)
	}
}

func TestInstallVerbs_StateRootInsideTheRepoIsRefused(t *testing.T) {
	env, repo, _ := installFixture(t)
	t.Setenv("TRELLIS_STATE", filepath.Join(repo, "state"))
	if code, _, errs := runInstall(env, "install"); code != 1 || !strings.Contains(errs, "is inside the repo") {
		t.Errorf("%d %q", code, errs)
	}
}

func TestInstall_PinnedBinaryRunsTheHook(t *testing.T) {
	bin := build(t)
	repo := testiso.Repo(t)
	state := filepath.Join(t.TempDir(), "state")

	cmd := exec.Command(bin, "install", "--repo", repo)
	cmd.Env = append(os.Environ(), "TRELLIS_STATE="+state)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	ext := filepath.Ext(bin)
	pinned := filepath.Join(state, "bin", "dev", "trellis"+ext)
	hook := exec.Command(pinned, "hook", "SessionStart")
	hook.Env = append(os.Environ(), "TRELLIS_STATE="+state)
	hook.Stdin = strings.NewReader(`{"session_id":"s","cwd":` + quote(repo) + `}`)
	if out, err := hook.CombinedOutput(); err != nil || len(out) != 0 {
		t.Errorf("pinned hook: %v %q", err, out)
	}
	doctor := exec.Command(pinned, "doctor", "--repo", repo)
	doctor.Env = append(os.Environ(), "TRELLIS_STATE="+state)
	if out, err := doctor.CombinedOutput(); err != nil || string(out) != "doctor: ok\n" {
		t.Errorf("doctor from the pinned copy: %v %q", err, out)
	}
}
