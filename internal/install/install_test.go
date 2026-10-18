package install

import (
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

// foreignSettings has two foreign hook entries, odd spacing, a numeric 1.0,
// and keys in an order no encoder would produce.
const foreignSettings = testiso.ForeignSettings

// settingsDoc is the part of a settings file the install tests read back.
type settingsDoc struct {
	Hooks map[string][]Entry `json:"hooks"`
}

type fixture struct {
	repo, root, exe string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "src", "trellis")
	testiso.Write(t, exe, "binary-bytes")
	return fixture{repo: testiso.Repo(t), root: filepath.Join(dir, "state"), exe: exe}
}

func (f fixture) opts(version string, dry bool) Options {
	return Options{Repo: f.repo, Root: f.root, Exe: f.exe, Version: version, Dry: dry}
}

func (f fixture) settings() string { return SettingsPath(f.repo) }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// hashTree is the sha256 of every file under each dir, keyed by path.
func hashTree(t *testing.T, dirs ...string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				out[p+"/"] = [32]byte{}
				return nil
			}
			b, err := os.ReadFile(p)
			out[p] = sha256.Sum256(b)
			return err
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	return out
}

func TestInstall_AddsTrellisEntriesOnly(t *testing.T) {
	f := newFixture(t)
	testiso.Write(t, f.settings(), foreignSettings)

	actions, err := Install(f.opts("v0.0.2", false))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(f.root, "bin", "v0.0.2", "trellis")
	want := []string{
		"copy binary " + bin,
		"add hook PreToolUse", "add hook PostToolUse", "add hook Stop",
		"add hook SessionStart", "add hook SessionEnd", "add hook UserPromptSubmit",
	}
	// Stop and PreToolUse already exist as foreign arrays: still "add hook".
	if strings.Join(actions, "\n") != strings.Join(want, "\n") {
		t.Errorf("actions:\n%s\nwant:\n%s", strings.Join(actions, "\n"), strings.Join(want, "\n"))
	}
	if got := readFile(t, bin); got != "binary-bytes" {
		t.Errorf("pinned binary holds %q", got)
	}

	got := readFile(t, f.settings())
	// Foreign bytes survive verbatim and in order.
	prev := -1
	for _, frag := range []string{
		`{ "zeta":{"b":1,"a":2},`,
		`"model" : "opus",`,
		`"ratio": 1.0,`,
		`"Stop":[ {"hooks":[{"type":"command","command":"foreign-stop"}]}`,
		`"PreToolUse" : [{"matcher":"Bash","hooks":[{"type":"command","command":"foreign-pre","timeout":5}]}`,
		`"alpha": [3, 2.50, 1]`,
	} {
		i := strings.Index(got, frag)
		if i < 0 || i < prev {
			t.Fatalf("fragment %q missing or out of order in:\n%s", frag, got)
		}
		prev = i
	}

	var doc settingsDoc
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, got)
	}
	counts := map[string]int{}
	for ev, entries := range doc.Hooks {
		counts[ev] = len(entries)
	}
	wantCounts := map[string]int{"PreToolUse": 2, "PostToolUse": 1, "Stop": 2, "SessionStart": 1, "SessionEnd": 1, "UserPromptSubmit": 1}
	if len(counts) != len(wantCounts) {
		t.Fatalf("events %v, want %v", counts, wantCounts)
	}
	for ev, n := range wantCounts {
		if counts[ev] != n {
			t.Errorf("%s has %d entries, want %d", ev, counts[ev], n)
		}
	}
	pre := doc.Hooks["PreToolUse"]
	if pre[0].Hooks[0].Command != "foreign-pre" || pre[1].Matcher != "Edit|Write|Bash|PowerShell" || pre[1].Hooks[0].Timeout != 10 {
		t.Errorf("PreToolUse entries: %+v", pre)
	}
	if want := Command(bin, "PreToolUse"); pre[1].Hooks[0].Command != want {
		t.Errorf("command %q, want %q", pre[1].Hooks[0].Command, want)
	}
	if end := doc.Hooks["SessionEnd"][0]; end.Matcher != "" {
		t.Errorf("SessionEnd has matcher %q", end.Matcher)
	}
}

func TestInstall_CreatesSettingsWhenAbsent(t *testing.T) {
	f := newFixture(t)
	if _, err := Install(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	var doc settingsDoc
	if err := json.Unmarshal([]byte(readFile(t, f.settings())), &doc); err != nil || len(doc.Hooks) != 6 {
		t.Errorf("settings: %v, %d events", err, len(doc.Hooks))
	}
}

func TestInstall_DryChangesNothing(t *testing.T) {
	f := newFixture(t)
	testiso.Write(t, f.settings(), foreignSettings)
	testiso.Write(t, filepath.Join(f.repo, ".claude", "other.json"), "{}")
	before := hashTree(t, f.repo, f.root)

	actions, err := Install(f.opts("v0.0.2", true))
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 7 || actions[0] != "would copy binary "+filepath.Join(f.root, "bin", "v0.0.2", "trellis") || actions[1] != "would add hook PreToolUse" {
		t.Errorf("actions %q", actions)
	}
	after := hashTree(t, f.repo, f.root)
	if len(after) != len(before) {
		t.Errorf("file set changed: %d entries before, %d after", len(before), len(after))
	}
	for p, h := range before {
		if after[p] != h {
			t.Errorf("%s changed", p)
		}
	}
}

func TestInstall_DryOnAbsentSettingsCreatesNoDirectory(t *testing.T) {
	f := newFixture(t)
	if _, err := Install(f.opts("v0.0.2", true)); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Dir(f.settings()), f.root} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s exists after a dry run (%v)", p, err)
		}
	}
}

func TestInstall_RefusesBadSettingsBeforeCopyingAnything(t *testing.T) {
	f := newFixture(t)
	testiso.Write(t, f.settings(), `{"hooks":`)
	if _, err := Install(f.opts("v0.0.2", false)); err == nil {
		t.Fatal("no error for invalid JSON")
	}
	if _, err := os.Stat(f.root); !os.IsNotExist(err) {
		t.Errorf("state root created despite the error (%v)", err)
	}
	if got := readFile(t, f.settings()); got != `{"hooks":` {
		t.Errorf("settings rewritten to %q", got)
	}
}

func TestInstall_RefusesAVersionThatIsNotADirectoryName(t *testing.T) {
	f := newFixture(t)
	for _, v := range []string{"", "a/b", `a\b`, ".."} {
		if _, err := Install(f.opts(v, false)); err == nil {
			t.Errorf("version %q accepted", v)
		}
	}
}

func TestInstall_SecondRunKeepsAndANewVersionRepins(t *testing.T) {
	f := newFixture(t)
	testiso.Write(t, f.settings(), foreignSettings)
	if _, err := Install(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, f.settings())
	actions, err := Install(f.opts("v0.0.2", false))
	if err != nil {
		t.Fatal(err)
	}
	if actions[0] != "keep binary "+filepath.Join(f.root, "bin", "v0.0.2", "trellis") || actions[1] != "keep hook PreToolUse" {
		t.Errorf("actions %q", actions)
	}
	if readFile(t, f.settings()) != first {
		t.Error("a second install changed the settings")
	}
	actions, err = Install(f.opts("v0.0.3", false))
	if err != nil {
		t.Fatal(err)
	}
	if actions[1] != "update hook PreToolUse" {
		t.Errorf("actions %q", actions)
	}
	if got := readFile(t, f.settings()); strings.Contains(got, "v0.0.2") || strings.Count(got, "v0.0.3") != 6 {
		t.Errorf("settings still name the old version or lack the new:\n%s", got)
	}
}

func TestEject_LeavesOnlyForeignEntries(t *testing.T) {
	f := newFixture(t)
	testiso.Write(t, f.settings(), foreignSettings)
	if _, err := Install(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	actions, err := Eject(f.opts("v0.0.2", false))
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 6 || actions[0] != "remove hook Stop" {
		t.Errorf("actions %q", actions)
	}
	if got := readFile(t, f.settings()); got != foreignSettings {
		t.Errorf("after eject:\n%s\nwant:\n%s", got, foreignSettings)
	}
}

func TestEject_AfterInstallOnAnEmptyObjectLeavesTheObject(t *testing.T) {
	f := newFixture(t)
	testiso.Write(t, f.settings(), "{}\n")
	if _, err := Install(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	if _, err := Eject(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, f.settings()); got != "{}\n" {
		t.Errorf("got %q", got)
	}
}

func TestEject_NoSettingsFileIsNothingToDo(t *testing.T) {
	f := newFixture(t)
	actions, err := Eject(f.opts("v0.0.2", false))
	if err != nil || len(actions) != 0 {
		t.Errorf("actions %q, err %v", actions, err)
	}
	if _, err := os.Stat(filepath.Dir(f.settings())); !os.IsNotExist(err) {
		t.Errorf("eject created %s", filepath.Dir(f.settings()))
	}
}

func TestEject_IgnoresATrellisLookalikeOutsideTheStateRoot(t *testing.T) {
	f := newFixture(t)
	const own = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"\"/elsewhere/bin/v1/trellis\" hook Stop"}]}]}}`
	testiso.Write(t, f.settings(), own)
	actions, err := Eject(f.opts("v0.0.2", false))
	if err != nil || len(actions) != 0 || readFile(t, f.settings()) != own {
		t.Errorf("actions %q, err %v, settings %q", actions, err, readFile(t, f.settings()))
	}
}

// writeInstalled writes a settings file that has a pinned hook for version.
func writeInstalled(t *testing.T, f fixture, version string) {
	t.Helper()
	cmd, err := json.Marshal(Command(BinPath(f.root, version, ""), "Stop"))
	if err != nil {
		t.Fatal(err)
	}
	testiso.Write(t, f.settings(), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":`+string(cmd)+`}]}]}}`)
}

func TestDoctor_ReportsVersionMismatchAndUnwritableRoot(t *testing.T) {
	f := newFixture(t)
	writeInstalled(t, f, "v0.0.1")
	got := Doctor(f.opts("v0.0.2", false))
	if !contains(got, "plugin v0.0.1, binary v0.0.2") {
		t.Errorf("no version line in %q", got)
	}

	// A regular file where the state root should be.
	blocked := filepath.Join(t.TempDir(), "blocked")
	testiso.Write(t, blocked, "x")
	o := f.opts("v0.0.2", false)
	o.Root = blocked
	if got := Doctor(o); !contains(got, "state root not writable: "+blocked) {
		t.Errorf("no root line in %q", got)
	}
}

func TestDoctor_CleanBoxIsQuiet(t *testing.T) {
	f := newFixture(t)
	if _, err := Install(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	if got := Doctor(f.opts("v0.0.2", false)); len(got) != 0 {
		t.Errorf("problems on a clean box: %q", got)
	}
}

func TestDoctor_NotInstalledAndMissingBinary(t *testing.T) {
	f := newFixture(t)
	if got := Doctor(f.opts("v0.0.2", false)); !contains(got, "trellis is not installed: "+f.settings()) {
		t.Errorf("not-installed line missing in %q", got)
	}
	writeInstalled(t, f, "v0.0.2")
	if got := Doctor(f.opts("v0.0.2", false)); !contains(got, "pinned binary missing: "+BinPath(f.root, "v0.0.2", "")) {
		t.Errorf("missing-binary line absent in %q", got)
	}
}

func TestInstall_RefusesAnUnversionedBuildUnlessAllowed(t *testing.T) {
	f := newFixture(t)
	const msg = "refusing to pin an unversioned build; pass --allow-dev"
	for _, v := range []string{"dev", ""} {
		if _, err := Install(f.opts(v, false)); err == nil || err.Error() != msg {
			t.Errorf("version %q: error %v, want %q", v, err, msg)
		}
	}
	o := f.opts("dev", false)
	o.AllowDev = true
	actions, err := Install(o)
	if err != nil || actions[0] != "copy binary "+filepath.Join(f.root, "bin", "dev", "trellis") {
		t.Errorf("--allow-dev: %q, %v", actions, err)
	}
}

func TestInstall_KeepsAnExistingPinnedBinaryWhateverItHolds(t *testing.T) {
	f := newFixture(t)
	bin := filepath.Join(f.root, "bin", "v0.0.2", "trellis")
	testiso.Write(t, bin, "held open by a running hook")
	actions, err := Install(f.opts("v0.0.2", false))
	if err != nil || actions[0] != "keep binary "+bin {
		t.Fatalf("%q, %v", actions, err)
	}
	if got := readFile(t, bin); got != "held open by a running hook" {
		t.Errorf("pinned binary now holds %q", got)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(bin), ".pin-*"))
	if len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestInstall_StateRootWithASpaceStillRoundTrips(t *testing.T) {
	f := newFixture(t)
	f.root = filepath.Join(t.TempDir(), "my state")
	if _, err := Install(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, f.settings()); !strings.Contains(got, `\"`+filepath.ToSlash(f.root)+`/bin/v0.0.2/trellis\" hook Stop`) {
		t.Errorf("command not quoted around the spaced path:\n%s", got)
	}
	if got := Doctor(f.opts("v0.0.2", false)); len(got) != 0 {
		t.Errorf("doctor: %q", got)
	}
	if _, err := Eject(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, f.settings()); got != "{}\n" {
		t.Errorf("after eject: %q", got)
	}
}

func TestDoctor_NeverCreatesTheStateRoot(t *testing.T) {
	f := newFixture(t)
	got := Doctor(f.opts("v0.0.2", false))
	if !contains(got, "trellis is not installed: "+f.settings()) || contains(got, "state root not writable: "+f.root) {
		t.Errorf("doctor: %q", got)
	}
	if _, err := os.Stat(f.root); !os.IsNotExist(err) {
		t.Errorf("doctor created the state root (%v)", err)
	}
}

func contains(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
