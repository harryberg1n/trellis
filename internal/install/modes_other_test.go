//go:build !windows

package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstall_CreatesOwnerOnlyFilesAndDirectories(t *testing.T) {
	f := newFixture(t)
	if _, err := Install(f.optsIn(false)); err != nil {
		t.Fatal(err)
	}
	if err := RecordCreatedPin(f.root, "v0.0.2"); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(f.root, "bin", "v0.0.2"): 0o700,
		BinPath(f.root, "v0.0.2", ""):          0o700,
		f.settings():                           0o600,
		filepath.Join(f.root, "repos.txt"):     0o600,
		filepath.Join(f.repo, "trellis.toml"):  0o644,
		filepath.Join(f.root, "pins.txt"):      0o600,
		filepath.Dir(f.settings()):             0o755,
		f.root:                                 0o700,
		filepath.Join(f.root, "records"):       0o700,
	} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
}
