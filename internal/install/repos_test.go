package install

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/harryberg1n/trellis/internal/store"
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
