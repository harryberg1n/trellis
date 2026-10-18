//go:build !windows

package update

import (
	"path/filepath"

	"github.com/harryberg1n/trellis/internal/store"
)

// replaceExe replaces the binary at exe with data through a temp file beside
// it. A unix rename over a running file is allowed: the running image keeps
// its old inode.
func replaceExe(exe string, data []byte) error {
	if err := store.WriteAtomic(filepath.Dir(exe), filepath.Base(exe), data, store.WithMode(0o755)); err != nil {
		return notWritable(err)
	}
	return nil
}
