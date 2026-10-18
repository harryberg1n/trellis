package install

import (
	"path/filepath"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/store"
)

// recordLockTimeout is how long a record update waits for another install.
const recordLockTimeout = 30 * time.Second

// lockRecords runs fn holding the store's cross-process lock on the state
// root's record directory, so two installs cannot each read the record, add a
// line and write it back over the other's.
func lockRecords(root string, fn func() error) error {
	s, err := store.Open(filepath.Join(root, "records"), clock.Real(), store.WithLockTimeout(recordLockTimeout))
	if err != nil {
		return err
	}
	return s.Locked(func(*store.Tx) error { return fn() })
}
