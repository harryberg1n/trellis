package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/testiso"
)

// A .old copy that cannot be removed or renamed over is stood in for by a
// non-empty directory of that name: a real lock needs a running image.
func TestReplaceExe_AsideFailureIsAnErrorAndLeavesTheBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "trellis.exe")
	testiso.Write(t, exe, "old")
	testiso.Write(t, filepath.Join(exe+oldSuffix, "held"), "x")

	err := replaceExe(exe, []byte("new"))

	if err == nil || errors.Is(err, errNotWritable) {
		t.Fatalf("err = %v, want a plain failure to move the binary aside", err)
	}
	if got := read(t, exe); got != "old" {
		t.Errorf("the binary = %q, want it untouched", got)
	}
	entries, rerr := os.ReadDir(dir)
	if rerr != nil || len(entries) != 2 {
		t.Errorf("directory holds %v, %v; want only trellis.exe and trellis.exe.old", entries, rerr)
	}
}
