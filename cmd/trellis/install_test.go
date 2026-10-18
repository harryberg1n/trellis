package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/testiso"
)

// installFixture is a repo, a Claude config directory (CLAUDE_CONFIG_DIR points
// at it, with foreign settings in it when foreign is set), a state root and a
// stand-in binary, with TRELLIS_STATE pointing at the root. The clock is fake,
// so the backup of the settings file has a fixed name.
func installFixture(t *testing.T, foreign bool) (env installEnv, repo, state, config string) {
	t.Helper()
	repo = testiso.Repo(t)
	dir := t.TempDir()
	state = filepath.Join(dir, "state")
	config = filepath.Join(dir, "claude")
	t.Setenv("TRELLIS_STATE", state)
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	exe := filepath.Join(dir, "trellis")
	testiso.Write(t, exe, "stand-in")
	if foreign {
		testiso.Write(t, filepath.Join(config, "settings.json"), testiso.ForeignSettings)
	}
	return installEnv{Cwd: repo, Exe: exe, Clock: clock.Fake(time.Unix(1000, 0))}, repo, state, config
}

func runInstall(env installEnv, args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = runInstallVerb(args, &out, &errb, env)
	return code, out.String(), errb.String()
}

func lines(ss ...string) string { return strings.Join(ss, "\n") + "\n" }

func TestInstallVerbs_FreshFixtureLines(t *testing.T) {
	env, repo, state, config := installFixture(t, false)
	settings := filepath.Join(config, "settings.json")

	code, out, errs := runInstall(env, "install", "--allow-dev")
	want := lines(
		"copy binary "+filepath.Join(state, "bin", "dev", "trellis"),
		"add hook PreToolUse", "add hook PostToolUse", "add hook Stop",
		"add hook SessionStart", "add hook SessionEnd", "add hook UserPromptSubmit",
		"write "+filepath.Join(repo, "trellis.toml"),
	)
	if code != 0 || errs != "" || out != want {
		t.Errorf("install: exit %d, stderr %q, stdout:\n%s\nwant:\n%s", code, errs, out, want)
	}

	if code, out, errs := runInstall(env, "doctor"); code != 0 || out != "doctor: ok\n" || errs != "" {
		t.Errorf("doctor: exit %d, stdout %q, stderr %q", code, out, errs)
	}

	code, out, errs = runInstall(env, "eject")
	want = lines(
		"remove hook PreToolUse", "remove hook PostToolUse", "remove hook Stop",
		"remove hook SessionStart", "remove hook SessionEnd", "remove hook UserPromptSubmit",
		"keep trellis.toml: it is this repository's own committed file",
	)
	if code != 0 || errs != "" || out != want {
		t.Errorf("eject: exit %d, stderr %q, stdout:\n%s\nwant:\n%s", code, errs, out, want)
	}
	if b, _ := os.ReadFile(settings); string(b) != "{}\n" {
		t.Errorf("settings after eject: %q", b)
	}

	// The repository is still on: trellis.toml stays, the hooks are gone.
	code, out, _ = runInstall(env, "doctor")
	if code != 1 || out != "trellis is not installed: "+settings+"\n" {
		t.Errorf("doctor after eject: exit %d, stdout %q", code, out)
	}
}

func TestInstallVerbs_InstallDoctorEjectCycle(t *testing.T) {
	env, repo, state, config := installFixture(t, true)
	settings := filepath.Join(config, "settings.json")

	code, out, errs := runInstall(env, "install", "--dry", "--allow-dev")
	if code != 0 || errs != "" || !strings.HasPrefix(out, "would copy binary "+filepath.Join(state, "bin", "dev", "trellis")+"\nwould add hook PreToolUse\n") {
		t.Fatalf("dry install: %d %q %q", code, out, errs)
	}
	wantMissing := lines("trellis is not installed: "+settings, "trellis.toml missing: this repository is off")
	if code, out, _ := runInstall(env, "doctor"); code != 1 || out != wantMissing {
		t.Errorf("doctor before install: %d %q", code, out)
	}

	code, out, errs = runInstall(env, "install", "--allow-dev")
	if code != 0 || errs != "" || !strings.HasPrefix(out, "copy binary ") || !strings.Contains(out, "back up "+settings+" to "+settings+".pre-trellis-1000\n") || !strings.Contains(out, "add hook UserPromptSubmit\n") {
		t.Fatalf("install: %d %q %q", code, out, errs)
	}
	if code, out, errs := runInstall(env, "doctor"); code != 0 || out != "doctor: ok\n" || errs != "" {
		t.Errorf("doctor after install: %d %q %q", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(repo, "trellis.toml")); err != nil {
		t.Errorf("trellis.toml: %v", err)
	}

	code, out, _ = runInstall(env, "eject")
	if code != 0 || !strings.HasPrefix(out, "remove hook Stop\n") {
		t.Fatalf("eject: %d %q", code, out)
	}
	if b, _ := os.ReadFile(settings); string(b) != testiso.ForeignSettings {
		t.Errorf("settings after eject:\n%s", b)
	}
	if code, out, _ := runInstall(env, "eject"); code != 0 || out != lines("eject: nothing to remove", "keep trellis.toml: it is this repository's own committed file") {
		t.Errorf("second eject: %d %q", code, out)
	}
}

func TestInstallVerbs_RepoFlagAndErrors(t *testing.T) {
	env, repo, _, config := installFixture(t, true)
	elsewhere := installEnv{Cwd: t.TempDir(), Exe: env.Exe, Clock: env.Clock}
	if code, out, _ := runInstall(elsewhere, "doctor"); code != 1 || !strings.Contains(out, "trellis is not installed: "+filepath.Join(config, "settings.json")) {
		t.Errorf("outside a repo, doctor checks the user-level file alone: %d %q", code, out)
	}
	if code, _, errs := runInstall(elsewhere, "doctor", "--repo", elsewhere.Cwd); code != 1 || !strings.Contains(errs, "not in a git repository") {
		t.Errorf("--repo outside a repo: %d %q", code, errs)
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

func TestInstallVerbs_ConfigDirFlagOverridesTheEnvironment(t *testing.T) {
	env, _, _, config := installFixture(t, false)
	other := filepath.Join(t.TempDir(), "other")

	if code, out, errs := runInstall(env, "install", "--allow-dev", "--config-dir", other); code != 0 {
		t.Fatalf("install: %d %q %q", code, out, errs)
	}

	if _, err := os.Stat(filepath.Join(other, "settings.json")); err != nil {
		t.Errorf("--config-dir was not used: %v", err)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Errorf("the environment's config dir was written too (%v)", err)
	}
}

func TestInstallVerbs_DevBuildNeedsAllowDev(t *testing.T) {
	env, _, _, _ := installFixture(t, false)
	code, out, errs := runInstall(env, "install")
	if code != 1 || out != "" || errs != "trellis install: refusing to pin an unversioned build; pass --allow-dev\n" {
		t.Errorf("%d %q %q", code, out, errs)
	}
}

func TestInstallVerbs_StateRootInsideTheRepoIsRefused(t *testing.T) {
	env, repo, _, _ := installFixture(t, false)
	t.Setenv("TRELLIS_STATE", filepath.Join(repo, "state"))
	if code, _, errs := runInstall(env, "install", "--allow-dev"); code != 1 || !strings.Contains(errs, "is inside the repo") {
		t.Errorf("%d %q", code, errs)
	}
}

func TestInstall_PinnedBinaryRunsTheHook(t *testing.T) {
	bin := build(t)
	repo := testiso.Repo(t)
	state := filepath.Join(t.TempDir(), "state")
	config := filepath.Join(t.TempDir(), "claude")

	if code, out, errs := runBin(t, bin, state, "", []string{"install", "--allow-dev", "--repo", repo, "--config-dir", config}); code != 0 {
		t.Fatalf("install: exit %d\n%s%s", code, out, errs)
	}
	ext := filepath.Ext(bin)
	pinned := filepath.Join(state, "bin", "dev", "trellis"+ext)
	hookIn := string(testiso.HookPayload(t, kernel.HookSessionStart, "", repo, nil, testiso.WithSession("s")))
	if code, out, errs := runBin(t, pinned, state, hookIn, []string{"hook", "SessionStart"}); code != 0 || out != "" || errs != "" {
		t.Errorf("pinned hook: exit %d, stdout %q, stderr %q", code, out, errs)
	}
	if code, out, errs := runBin(t, pinned, state, "", []string{"doctor", "--repo", repo, "--config-dir", config}); code != 0 || out != "doctor: ok\n" || errs != "" {
		t.Errorf("doctor from the pinned copy: exit %d, stdout %q, stderr %q", code, out, errs)
	}
}
