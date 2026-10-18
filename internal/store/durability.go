package store

import (
	"errors"
	"os"
)

// Durability is whether a write is flushed to disk before the call returns.
type Durability int

const (
	// Durable fsyncs the log append and the checkpoint, as before.
	Durable Durability = iota
	// Lazy writes without fsync. The data is in the OS cache and survives a
	// crash of the process, not a power loss. The caller makes it durable
	// later with Store.Sync. The log is replayed into the checkpoint at the
	// next Step, so a checkpoint older than the log heals itself.
	Lazy
)

// SetDurability sets how this Tx's Append and Save write. A new Tx is Durable.
func (tx *Tx) SetDurability(d Durability) { tx.mode = d }

// SetFsync replaces the fsync the store uses. A test counts calls through it;
// nil is not allowed.
func (s *Store) SetFsync(f func(*os.File) error) { s.fsync = f }

// Sync fsyncs the log file and the checkpoint file, whichever exist. It makes
// everything written Lazy so far durable.
func (s *Store) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for _, p := range []string{s.logPath(), s.statePath()} {
		f, err := openSharedRW(p) // FlushFileBuffers needs write access
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.fsync(f); err != nil {
			errs = append(errs, err)
		}
		if err := f.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
