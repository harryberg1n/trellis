package store

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// LockTimeout is the default for how long an event waits for the repo lock
	// before it is parked.
	LockTimeout  = 2 * time.Second
	lockPollWait = 10 * time.Millisecond
)

// ErrLockTimeout means the repo lock was not free within LockTimeout.
var ErrLockTimeout = errors.New("store: lock timeout")

func openLockFile(dir string) (*os.File, error) {
	return os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
}

func releaser(f *os.File) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			unlock(f)
			_ = f.Close() // release has no error to return and the lock is gone with the handle
		})
	}
}

// acquire takes the cross-process file lock, polling on the store clock so a
// test drives the wait by advancing a fake. The returned func releases it.
func (s *Store) acquire() (release func(), err error) {
	f, err := openLockFile(s.dir)
	if err != nil {
		return nil, err
	}
	deadline := s.clock.Now().Add(s.lockTimeout)
	for {
		ok, err := tryLock(f)
		if err != nil {
			_ = f.Close() // the lock error is the one to report
			return nil, err
		}
		if ok {
			return releaser(f), nil
		}
		if !s.clock.Now().Before(deadline) {
			_ = f.Close() // nothing was written; the timeout is the one to report
			return nil, ErrLockTimeout
		}
		<-s.clock.After(lockPollWait)
	}
}

// Hold takes only the file lock, once, through its own handle, and fails
// at once if it is held. It stands in for another process in tests.
func (s *Store) Hold() (release func(), err error) {
	f, err := openLockFile(s.dir)
	if err != nil {
		return nil, err
	}
	ok, err := tryLock(f)
	if err != nil || !ok {
		_ = f.Close() // nothing was written; the lock error is the one to report
		if err == nil {
			err = errors.New("store: lock is held")
		}
		return nil, err
	}
	return releaser(f), nil
}
