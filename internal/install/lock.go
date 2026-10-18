package install

import (
	"os"
	"path/filepath"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/store"
)

// recordLockTimeout is how long a record update waits for another install.
const recordLockTimeout = 30 * time.Second

// RecordOption changes how a record update runs.
type RecordOption func(*recordConfig)

type recordConfig struct{ clock clock.Clock }

// WithClock paces the wait for the records lock on c; the default is the wall
// clock.
func WithClock(c clock.Clock) RecordOption { return func(r *recordConfig) { r.clock = c } }

// lockRecords runs fn holding the store's cross-process lock on the state
// root's record directory, so two installs cannot each read the record, add a
// line and write it back over the other's. The root and the directory are made
// here, owner-only, before the store would make them.
func lockRecords(root string, fn func() error, opts ...RecordOption) error {
	cfg := recordConfig{clock: clock.Real()}
	for _, o := range opts {
		o(&cfg)
	}
	dir := filepath.Join(root, "records")
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	s, err := store.Open(dir, cfg.clock, store.WithLockTimeout(recordLockTimeout))
	if err != nil {
		return err
	}
	return s.Locked(func(*store.Tx) error { return fn() })
}
