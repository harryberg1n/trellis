package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/testiso"
)

// withTrellisEntries is a project-local settings file as an older install left
// it: the foreign settings with the six trellis entries wired in.
func withTrellisEntries(t *testing.T, f fixture, foreign string) string {
	t.Helper()
	scratch := f
	scratch.config = filepath.Join(t.TempDir(), "scratch")
	testiso.Write(t, scratch.settings(), foreign)
	if _, err := Wire(scratch.opts("v0.0.1", false)); err != nil {
		t.Fatal(err)
	}
	return readFile(t, scratch.settings())
}

func TestInstall_UserLevelFileGainsSixEntriesAndKeepsTheForeignOnesInOrder(t *testing.T) {
	f := newFixture(t)
	testiso.Write(t, f.settings(), foreignSettings)

	if _, err := Install(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, f.settings())
	if n := strings.Count(got, "/bin/v0.0.2/trellis' hook "); n != 6 {
		t.Errorf("%d trellis commands in the user-level file, want 6:\n%s", n, got)
	}
	for _, frag := range []string{`{ "zeta":{"b":1,"a":2},`, `"ratio": 1.0,`, `{"type":"command","command":"foreign-stop"}`, `"command":"foreign-pre","timeout":5`} {
		if !strings.Contains(got, frag) {
			t.Errorf("foreign fragment %q lost:\n%s", frag, got)
		}
	}
	if zeta, model := strings.Index(got, `"zeta"`), strings.Index(got, `"model"`); zeta < 0 || model < zeta {
		t.Errorf("foreign keys out of order:\n%s", got)
	}
}

func TestInstall_ASecondRunChangesNothingAndMakesNoSecondBackup(t *testing.T) {
	f := newFixture(t)
	testiso.Write(t, f.settings(), foreignSettings)
	o := f.optsIn(false)
	o.Clock = clock.Fake(time.Unix(1000, 0))
	if _, err := Install(o); err != nil {
		t.Fatal(err)
	}
	before := hashTree(t, f.config, f.root, f.repo)

	o.Clock = clock.Fake(time.Unix(2000, 0))
	if _, err := Install(o); err != nil {
		t.Fatal(err)
	}

	after := hashTree(t, f.config, f.root, f.repo)
	if len(after) != len(before) {
		t.Errorf("the file set changed: %d entries before, %d after", len(before), len(after))
	}
	for p, h := range before {
		if after[p] != h {
			t.Errorf("%s changed", p)
		}
	}
	backups, _ := filepath.Glob(f.settings() + ".pre-trellis-*")
	if len(backups) != 1 || backups[0] != f.settings()+".pre-trellis-1000" {
		t.Fatalf("backups %v, want only settings.json.pre-trellis-1000", backups)
	}
	if got := readFile(t, backups[0]); got != foreignSettings {
		t.Errorf("backup holds %q, want the file as it was", got)
	}
}

func TestInstall_AFreshConfigDirHasNothingToBackUp(t *testing.T) {
	f := newFixture(t)
	if _, err := Install(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	if backups, _ := filepath.Glob(f.settings() + ".pre-trellis-*"); len(backups) != 0 {
		t.Errorf("backups %v, want none for a file that was not there", backups)
	}
}

func TestEject_LeavesTheUserLevelFileAsItWasBeforeTheFirstInstall(t *testing.T) {
	f := newFixture(t)
	testiso.Write(t, f.settings(), foreignSettings)
	if _, err := Install(f.optsIn(false)); err != nil {
		t.Fatal(err)
	}

	if _, err := Eject(f.optsIn(false)); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, f.settings()); got != foreignSettings {
		t.Errorf("after eject:\n%s\nwant:\n%s", got, foreignSettings)
	}
}

func TestInstall_RemovesTrellisEntriesFromTheProjectLocalFilesOfThePrimaryAndEachTree(t *testing.T) {
	f := newFixture(t)
	tree := testiso.AddTree(t, f.repo, "feat-tree", "feat")
	old := withTrellisEntries(t, f, foreignSettings)
	for _, dir := range []string{f.repo, tree} {
		testiso.Write(t, filepath.Join(dir, ".claude", "settings.local.json"), old)
	}

	actions, err := Install(f.optsIn(false))
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{f.repo, tree} {
		file := filepath.Join(dir, ".claude", "settings.local.json")
		if got := readFile(t, file); got != foreignSettings {
			t.Errorf("%s:\n%s\nwant the foreign settings byte for byte:\n%s", file, got, foreignSettings)
		}
		if want := "remove hook Stop from " + file; !contains(actions, want) {
			t.Errorf("no action %q in %q", want, actions)
		}
	}
}

func TestInstall_AProjectLocalFileLeftWithNothingOfItsOwnStays(t *testing.T) {
	f := newFixture(t)
	old := withTrellisEntries(t, f, "{}\n")
	file := filepath.Join(f.repo, ".claude", "settings.local.json")
	testiso.Write(t, file, old)

	if _, err := Install(f.optsIn(false)); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, file); got != "{}\n" {
		t.Errorf("%s = %q, want the empty object, still there", file, got)
	}
}

func TestInstall_WritesTrellisTomlWhenMissingAndKeepsItWhenPresent(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(f.repo, "trellis.toml")

	actions, err := Install(f.optsIn(false))
	if err != nil {
		t.Fatal(err)
	}

	const want = "# trellis is on in this repository.\n# Commit this file; a repository without it is off.\n"
	if got := readFile(t, marker); got != want {
		t.Errorf("trellis.toml = %q, want %q", got, want)
	}
	if !contains(actions, "write "+marker) {
		t.Errorf("no action %q in %q", "write "+marker, actions)
	}

	testiso.Write(t, marker, "# the repository's own\n")
	actions, err = Install(f.optsIn(false))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, marker); got != "# the repository's own\n" {
		t.Errorf("trellis.toml = %q, want it kept", got)
	}
	if !contains(actions, "keep trellis.toml") {
		t.Errorf("no action %q in %q", "keep trellis.toml", actions)
	}
}

func TestInstall_DryWritesNoMarkerAndPrunesNothing(t *testing.T) {
	f := newFixture(t)
	old := withTrellisEntries(t, f, foreignSettings)
	file := filepath.Join(f.repo, ".claude", "settings.local.json")
	testiso.Write(t, file, old)

	actions, err := Install(f.optsIn(true))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(f.repo, "trellis.toml")); !os.IsNotExist(err) {
		t.Errorf("a dry install wrote trellis.toml (%v)", err)
	}
	if got := readFile(t, file); got != old {
		t.Errorf("a dry install changed the project-local file")
	}
	if want := "would write " + filepath.Join(f.repo, "trellis.toml"); !contains(actions, want) {
		t.Errorf("no action %q in %q", want, actions)
	}
}

func TestEject_RemovesTheProjectLocalEntriesAndSaysTrellisTomlStays(t *testing.T) {
	f := newFixture(t)
	old := withTrellisEntries(t, f, foreignSettings)
	file := filepath.Join(f.repo, ".claude", "settings.local.json")
	testiso.Write(t, file, old)
	testiso.Write(t, filepath.Join(f.repo, "trellis.toml"), "# on\n")

	actions, err := Eject(f.optsIn(false))
	if err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, file); got != foreignSettings {
		t.Errorf("project-local file after eject:\n%s", got)
	}
	if got := readFile(t, filepath.Join(f.repo, "trellis.toml")); got != "# on\n" {
		t.Errorf("trellis.toml = %q, want it left alone", got)
	}
	const want = "keep trellis.toml: it is this repository's own committed file"
	if last := actions[len(actions)-1]; last != want {
		t.Errorf("last line %q, want %q", last, want)
	}
}

func TestDoctor_SaysARepositoryWithoutTrellisTomlIsOff(t *testing.T) {
	f := newFixture(t)
	if _, err := Wire(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}

	got := Doctor(f.optsIn(false))

	if len(got) != 1 || got[0] != "trellis.toml missing: this repository is off" {
		t.Errorf("doctor = %q, want only the off line", got)
	}
}

func TestDoctor_NamesAProjectLocalFileThatStillHoldsTrellisEntries(t *testing.T) {
	f := newFixture(t)
	tree := testiso.AddTree(t, f.repo, "feat-tree", "feat")
	if _, err := Install(f.optsIn(false)); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(tree, ".claude", "settings.local.json")
	testiso.Write(t, file, withTrellisEntries(t, f, foreignSettings))

	got := Doctor(f.optsIn(false))

	want := file + " still holds trellis hooks (every event fires twice): run trellis install"
	if len(got) != 1 || got[0] != want {
		t.Errorf("doctor = %q, want [%q]", got, want)
	}
}

func TestDoctor_AnInstalledRepositoryWithTheMarkerIsClean(t *testing.T) {
	f := newFixture(t)
	if _, err := Install(f.optsIn(false)); err != nil {
		t.Fatal(err)
	}
	if got := Doctor(f.optsIn(false)); len(got) != 0 {
		t.Errorf("doctor = %q, want no problems", got)
	}
}
