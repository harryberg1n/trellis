package install

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestInstall_RecordsTheRepo(t *testing.T) {
	f := newFixture(t)

	if _, err := Install(f.opts("v0.0.2", true)); err != nil {
		t.Fatal(err)
	}
	if got, err := Repos(f.root); err != nil || len(got) != 0 {
		t.Fatalf("a dry install recorded %v, %v; want nothing", got, err)
	}

	for range 2 {
		if _, err := Install(f.opts("v0.0.2", false)); err != nil {
			t.Fatal(err)
		}
	}
	want := store.Canonical(f.repo)
	got, err := Repos(f.root)
	if err != nil || !reflect.DeepEqual(got, []string{want}) {
		t.Fatalf("Repos = %v, %v; want [%s] once", got, err, want)
	}
	if file := readFile(t, filepath.Join(f.root, "repos.txt")); file != want+"\n" {
		t.Errorf("repos.txt = %q, want %q", file, want+"\n")
	}
}

func TestInstall_InATreeOfAnUninstalledPrimaryRecordsNothingAndSaysSo(t *testing.T) {
	f := newFixture(t)
	tree := testiso.AddTree(t, f.repo, "feat-tree", "feat")
	o := f.opts("v0.0.2", false)
	o.Repo = tree

	actions, err := Install(o)
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := Repos(f.root); len(got) != 0 {
		t.Errorf("Repos = %v, want nothing recorded", got)
	}
	if got, err := ReferencedRepos(f.root); err != nil || !reflect.DeepEqual(got, []string{store.Canonical(f.repo)}) {
		t.Errorf("ReferencedRepos = %v, %v; want the primary, referenced only", got, err)
	}
	want := "not re-installed by update (primary checkout not installed; its pins are kept): run trellis install in " + store.Canonical(f.repo) + " to include this repository"
	if last := actions[len(actions)-1]; last != want {
		t.Errorf("last action = %q, want %q", last, want)
	}
	if _, err := os.Stat(SettingsPath(f.repo)); !os.IsNotExist(err) {
		t.Errorf("the primary's settings were touched: %v", err)
	}
}

func TestInstall_InATreeOfAnInstalledPrimaryRecordsThePrimaryOnce(t *testing.T) {
	f := newFixture(t)
	if _, err := Install(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	tree := testiso.AddTree(t, f.repo, "feat-tree", "feat")
	o := f.opts("v0.0.2", false)
	o.Repo = tree

	for range 2 {
		actions, err := Install(o)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range actions {
			if strings.HasPrefix(a, "not recorded") {
				t.Errorf("unexpected action %q", a)
			}
		}
	}

	got, err := Repos(f.root)
	if want := []string{store.Canonical(f.repo)}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Repos = %v, %v; want %v once", got, err, want)
	}
}

func TestCreatedPins_RecordOncePerVersion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if got, err := CreatedPins(root); err != nil || len(got) != 0 {
		t.Fatalf("CreatedPins with no record = %v, %v; want none", got, err)
	}
	for _, v := range []string{"v0.0.4", "v0.0.5", "v0.0.4"} {
		if err := RecordCreatedPin(root, v); err != nil {
			t.Fatal(err)
		}
	}
	got, err := CreatedPins(root)
	if want := []string{"v0.0.4", "v0.0.5"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("CreatedPins = %v, %v; want %v", got, err, want)
	}
	if file := readFile(t, filepath.Join(root, "pins.txt")); file != "v0.0.4\nv0.0.5\n" {
		t.Errorf("pins.txt = %q", file)
	}
}

func TestPin_DoesNotRecordACreatedPin(t *testing.T) {
	f := newFixture(t)
	if _, _, err := Pin(f.root, "v0.0.9", f.exe); err != nil {
		t.Fatal(err)
	}
	if got, _ := CreatedPins(f.root); len(got) != 0 {
		t.Errorf("Pin recorded %v; only update records what it created", got)
	}
}

func TestEject_ForgetsTheRepo(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(t.TempDir(), "other")
	if err := RecordRepo(f.root, other); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}

	if got, _ := Repos(f.root); len(got) != 2 {
		t.Fatalf("before eject Repos = %v, want 2", got)
	}

	if _, err := Eject(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	got, err := Repos(f.root)
	if err != nil || !reflect.DeepEqual(got, []string{store.Canonical(other)}) {
		t.Fatalf("Repos after eject = %v, %v; want only %s", got, err, store.Canonical(other))
	}
}

func TestRepos_AbsentFileIsNoRepos(t *testing.T) {
	got, err := Repos(filepath.Join(t.TempDir(), "none"))
	if err != nil || len(got) != 0 {
		t.Errorf("Repos = %v, %v; want none", got, err)
	}
}

func TestReferencedVersions_ReadsWhatTheHooksPointAt(t *testing.T) {
	f := newFixture(t)
	if _, err := Install(f.opts("v0.0.2", false)); err != nil {
		t.Fatal(err)
	}
	got, err := ReferencedVersions(f.root, f.repo)
	if err != nil || !reflect.DeepEqual(got, []string{"v0.0.2"}) {
		t.Errorf("ReferencedVersions = %v, %v; want [v0.0.2]", got, err)
	}
	if err := os.Remove(f.settings()); err != nil {
		t.Fatal(err)
	}
	if got, err := ReferencedVersions(f.root, f.repo); err != nil || len(got) != 0 {
		t.Errorf("with no settings: %v, %v; want none", got, err)
	}
}

func TestPin_CopiesOnceAndKeepsAnExistingPin(t *testing.T) {
	f := newFixture(t)

	bin, copied, err := Pin(f.root, "v0.0.9", f.exe)
	if err != nil || !copied || bin != BinPath(f.root, "v0.0.9", filepath.Ext(f.exe)) {
		t.Fatalf("Pin = %q, %v, %v; want a copy at the pinned path", bin, copied, err)
	}
	if got := readFile(t, bin); got != "binary-bytes" {
		t.Errorf("pinned content = %q, want binary-bytes", got)
	}
	if err := os.WriteFile(bin, []byte("first"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, copied, err := Pin(f.root, "v0.0.9", f.exe); err != nil || copied {
		t.Errorf("second Pin copied=%v err=%v; want it kept", copied, err)
	}
	if got := readFile(t, bin); got != "first" {
		t.Errorf("an existing pin was overwritten: %q", got)
	}
	if _, _, err := Pin(f.root, "../x", f.exe); err == nil {
		t.Error("a version naming a parent directory was accepted")
	}
}

func TestRecordRepo_ParallelRecordsLoseNoLine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	repos := make([]string, 0, 8)
	for range 8 {
		repos = append(repos, testiso.Repo(t))
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(repos)*2)
	for i, r := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- RecordRepo(root, r)
			errs <- RecordCreatedPin(root, fmt.Sprintf("v0.0.%d", i))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := Repos(root)
	if err != nil || len(got) != 8 {
		t.Fatalf("Repos = %v, %v; want 8 lines", got, err)
	}
	pins, err := CreatedPins(root)
	if err != nil || len(pins) != 8 {
		t.Fatalf("CreatedPins = %v, %v; want 8 lines", pins, err)
	}
}

func TestSweepReferences_ReadsLinkedWorktreesOfAPrimaryThatHasNoHooks(t *testing.T) {
	f := newFixture(t)
	tree := testiso.AddTree(t, f.repo, "feat-tree", "feat")
	o := f.opts("v0.0.7", false)
	o.Repo = tree
	if _, err := Install(o); err != nil {
		t.Fatal(err)
	}

	own, err := ReferencedVersions(f.root, f.repo)
	if err != nil || len(own) != 0 {
		t.Fatalf("the primary references %v, %v; want nothing of its own", own, err)
	}
	got, err := SweepReferences(f.root, f.repo)
	if want := []string{"v0.0.7"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("SweepReferences = %v, %v; want %v", got, err, want)
	}
	trees, err := LinkedWorktrees(f.repo)
	if want := []string{store.Canonical(tree)}; err != nil || !reflect.DeepEqual(trees, want) {
		t.Errorf("LinkedWorktrees = %v, %v; want %v", trees, err, want)
	}
}
