package testiso

import (
	"os"
	"path/filepath"
	"testing"
)

// AltSpelling is another path for the existing directory dir: its 8.3 short
// name on Windows when it has one, else a symlink to it. The test is skipped
// when the platform gives no second spelling. It lets a test exercise code
// that compares paths from outside with a repo's canonical ones, whichever
// machine runs it.
func AltSpelling(t testing.TB, dir string) string {
	t.Helper()
	if alt := shortSpelling(dir); alt != "" && alt != dir {
		return alt
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("no second spelling of %s: %v", dir, err)
	}
	return link
}
