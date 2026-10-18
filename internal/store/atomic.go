package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/harryberg1n/trellis/internal/clock"
)

// atomicConfig is what WriteAtomic's options set.
type atomicConfig struct {
	sync    func(*os.File) error
	rename  func(oldpath, newpath string) error
	clock   clock.Clock
	retry   func(error) bool
	mode    fs.FileMode
	soft    bool // a chmod failure is ignored
	pattern string
}

// AtomicOption changes how WriteAtomic publishes a file.
type AtomicOption func(*atomicConfig)

// WithSync flushes the temp file with f before it is renamed into place. Without
// it the data is in the OS cache when the call returns.
func WithSync(f func(*os.File) error) AtomicOption { return func(c *atomicConfig) { c.sync = f } }

// WithRename publishes with f instead of the platform rename: the seam a test injects a
// failure through.
func WithRename(f func(oldpath, newpath string) error) AtomicOption {
	return func(c *atomicConfig) { c.rename = f }
}

// WithMode sets the published file's permissions, failing the write if they
// cannot be set; the default is the owner-only
// mode os.CreateTemp gives.
func WithMode(m fs.FileMode) AtomicOption { return func(c *atomicConfig) { c.mode = m } }

// WithSoftMode is WithMode for a file whose mode is a courtesy: a chmod that
// fails is ignored and the file is published with the temp file's mode.
func WithSoftMode(m fs.FileMode) AtomicOption {
	return func(c *atomicConfig) { c.mode, c.soft = m, true }
}

// WithTempPattern names the temp file by an os.CreateTemp pattern; the default
// is the target's name, a dot, a random string and ".tmp".
func WithTempPattern(p string) AtomicOption { return func(c *atomicConfig) { c.pattern = p } }

// WriteAtomic writes data to dir/name through a temp file in dir, then renames
// it over the target, so a reader sees the old file or the whole new one. On any
// failure the old file stays as it was and the temp file is removed. dir must
// exist.
func WriteAtomic(dir, name string, data []byte, opts ...AtomicOption) error {
	c := atomicConfig{rename: replaceFile, clock: clock.Real(), retry: renameRetryable, pattern: name + ".*.tmp"}
	for _, o := range opts {
		o(&c)
	}
	f, err := os.CreateTemp(dir, c.pattern)
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil && c.sync != nil {
		err = c.sync(f)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && c.mode != 0 {
		if cerr := os.Chmod(tmp, c.mode); cerr != nil && !c.soft {
			err = cerr
		}
	}
	if err == nil {
		dst := filepath.Join(dir, name)
		err = retryRename(c.clock, func() error { return c.rename(tmp, dst) }, c.retry)
	}
	if err != nil {
		if rerr := os.Remove(tmp); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			err = errors.Join(err, rerr)
		}
	}
	return err
}

// WithClock paces the retry of a rename the OS refuses; the default is the
// wall clock. The store passes its own so a test drives the pauses.
func WithClock(c clock.Clock) AtomicOption { return func(a *atomicConfig) { a.clock = c } }

// withRetryable replaces which rename errors are tried again: the seam a test
// uses to make an error portable.
func withRetryable(f func(error) bool) AtomicOption { return func(c *atomicConfig) { c.retry = f } }
