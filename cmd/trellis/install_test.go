package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/testiso"
)

// installFixture is a repo with foreign settings, a state root and a stand-in
// binary, with TRELLIS_STATE pointing at the root.
func installFixture(t *testing.T) (env installEnv, repo, state string) {
	t.Helper()
	repo = testiso.Repo(t)
	dir := t.TempDir()
	state = filepath.Join(dir, "state")
	t.Setenv("TRELLIS_STATE", state)
	exe := filepath.Join(dir, "trellis")
	testiso.Write(t, exe, "stand-in")
	testiso.Write(t, filepath.Join(repo, ".claude", "settings.local.json"), testiso.ForeignSettings)
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

	code, out, errs := runInstall(env, "install", "--dry", "--allow-dev")
	if code != 0 || errs != "" || !strings.HasPrefix(out, "would copy binary "+filepath.Join(state, "bin", "dev", "trellis")+"\nwould add hook PreToolUse\n") {
		t.Fatalf("dry install: %d %q %q", code, out, errs)
	}
	if code, out, _ := runInstall(env, "doctor"); code != 1 || out != "trellis is not installed: "+settings+"\n" {
		t.Errorf("doctor before install: %d %q", code, out)
	}

	code, out, errs = runInstall(env, "install", "--allow-dev")
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
	if b, _ := os.ReadFile(settings); string(b) != testiso.ForeignSettings {
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
	if code, out, _ := runInstall(elsewhere, "install", "--repo", repo, "--dry", "--allow-dev"); code != 0 || !strings.Contains(out, "would add hook Stop") {
		t.Errorf("--repo: %d %q", code, out)
	}
	if code, _, errs := runInstall(env, "eject", "--dry"); code != 2 || !strings.Contains(errs, "--dry and --allow-dev are install flags") {
		t.Errorf("eject --dry: %d %q", code, errs)
	}
	if code, _, _ := runInstall(env, "install", "--bogus"); code != 2 {
		t.Errorf("bad flag exits %d, want 2", code)
	}
}

func TestInstallVerbs_DevBuildNeedsAllowDev(t *testing.T) {
	env, _, _ := installFixture(t)
	code, out, errs := runInstall(env, "install")
	if code != 1 || out != "" || errs != "trellis install: refusing to pin an unversioned build; pass --allow-dev\n" {
		t.Errorf("%d %q %q", code, out, errs)
	}
}

func TestInstallVerbs_StateRootInsideTheRepoIsRefused(t *testing.T) {
	env, repo, _ := installFixture(t)
	t.Setenv("TRELLIS_STATE", filepath.Join(repo, "state"))
	if code, _, errs := runInstall(env, "install", "--allow-dev"); code != 1 || !strings.Contains(errs, "is inside the repo") {
		t.Errorf("%d %q", code, errs)
	}
}

func TestInstall_PinnedBinaryRunsTheHook(t *testing.T) {
	bin := build(t)
	repo := testiso.Repo(t)
	state := filepath.Join(t.TempDir(), "state")

	if code, out, errs := runBin(t, bin, state, "", []string{"install", "--allow-dev", "--repo", repo}); code != 0 {
		t.Fatalf("install: exit %d\n%s%s", code, out, errs)
	}
	ext := filepath.Ext(bin)
	pinned := filepath.Join(state, "bin", "dev", "trellis"+ext)
	hookIn := string(testiso.HookPayload(t, kernel.HookSessionStart, "", repo, nil, testiso.WithSession("s")))
	if code, out, errs := runBin(t, pinned, state, hookIn, []string{"hook", "SessionStart"}); code != 0 || out != "" || errs != "" {
		t.Errorf("pinned hook: exit %d, stdout %q, stderr %q", code, out, errs)
	}
	if code, out, errs := runBin(t, pinned, state, "", []string{"doctor", "--repo", repo}); code != 0 || out != "doctor: ok\n" || errs != "" {
		t.Errorf("doctor from the pinned copy: exit %d, stdout %q, stderr %q", code, out, errs)
	}
}
