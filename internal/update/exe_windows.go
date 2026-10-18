package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/harryberg1n/trellis/internal/store"
)

// replaceExe replaces the binary at exe with data. Windows will not let a
// running file be overwritten or deleted, but it lets it be renamed: the new
// binary is staged beside it, the old one is renamed to <name>.old, and the new
// one is renamed in. The next update removes the .old copy. A failure after the
// old one moved aside puts it back.
func replaceExe(exe string, data []byte) error {
	dir, name := filepath.Dir(exe), filepath.Base(exe)
	next := name + ".new"
	if err := store.WriteAtomic(dir, next, data, store.WithMode(0o755)); err != nil {
		return notWritable(err)
	}
	nextPath, old := filepath.Join(dir, next), exe+oldSuffix
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return errors.Join(fmt.Errorf("moving %s aside: %w", exe, err), os.Remove(nextPath))
	}
	if err := os.Rename(nextPath, exe); err != nil {
		return errors.Join(fmt.Errorf("moving the new binary to %s: %w", exe, err), os.Rename(old, exe), os.Remove(nextPath))
	}
	return nil
}
