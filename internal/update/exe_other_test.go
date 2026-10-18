//go:build !windows

package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/install"
	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestUpdate_UnwritableBinaryOnPathNamesTheNewOne(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	w := newWorld(t)
	dir := filepath.Join(w.dir, "ro")
	w.exe = filepath.Join(dir, "trellis")
	testiso.Write(t, w.exe, oldBinary)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := w.run(Options{}); err != nil {
		t.Fatal(err)
	}

	want := "the binary on PATH is " + store.Canonical(w.exe) + "; the new one is " + store.Canonical(install.BinPath(w.root, "v0.0.4", ""))
	if !contains(w.lines(), want) {
		t.Errorf("no line %q in:\n%s", want, w.out)
	}
}
