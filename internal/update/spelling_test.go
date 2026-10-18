package update

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/harryberg1n/trellis/internal/install"
	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestUpdate_BinaryOnPathInAnotherSpellingOfThePinsIsNotReplaced(t *testing.T) {
	w := newWorld(t)
	alias := testiso.AltSpelling(t, w.root)
	w.exe = filepath.Join(alias, "bin", "v0.0.3", "trellis")

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	want := "the binary on PATH is " + store.Canonical(w.exe) + "; the new one is " + store.Canonical(install.BinPath(w.root, "v0.0.4", ""))
	if !contains(w.lines(), want) {
		t.Errorf("no line %q in:\n%s", want, w.out)
	}
	if got := read(t, install.BinPath(w.root, "v0.0.3", "")); got != "v0.0.3-binary" {
		t.Errorf("the v0.0.3 pin = %q, want it untouched", got)
	}
}

func TestUpdate_SweepSeesReferencesThroughAnotherSpellingOfTheRoot(t *testing.T) {
	w := newWorld(t)
	w.updatePins("v0.0.1", "v0.0.2")
	w.oldPins("v0.0.5")
	w.alsoReference(w.repos[1], "v0.0.1")
	env := w.env()
	env.Root = testiso.AltSpelling(t, w.root)

	if err := Run(env, Options{}); err != nil {
		t.Fatal(err)
	}

	if got, want := w.pinned(), []string{"v0.0.1", "v0.0.3", "v0.0.4", "v0.0.5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pins = %v, want %v (v0.0.1 is referenced)", got, want)
	}
	// Re-installed through the other spelling, the hook was replaced, not added.
	if got := w.referenced(w.repos[0]); !reflect.DeepEqual(got, []string{"v0.0.4"}) {
		t.Errorf("%s hooks point at %v, want [v0.0.4] and no second entry", w.repos[0], got)
	}
}
