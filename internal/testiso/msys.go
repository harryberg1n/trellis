// Copied from aphrollo-tools internal/msys/msys.go at 12b3724e.
// Incident #1043: tests wrote into the real repository and the global git config.
// Differences from the copy: the package path; anchorExited is kept and
// stopAnchor ends the shell and waits for it, so the goroutine that waits on
// the anchor is gone before the run's goroutine-leak check (goleak).

// The MSYS anchor keeps the MSYS shell tools of a Windows box from taking a
// throwaway directory for their /tmp.
//
// Git Bash's /tmp is the mount "none /tmp usertemp": the user temp directory
// of the FIRST MSYS process of the session, remembered in MSYS's shared memory
// for as long as any MSYS process is alive. A test binary (or a mutation run)
// that points TEMP at a directory of its own and starts a shell or a git hook
// while no other MSYS process is alive hands that directory to every shell
// started after it; when the run removes the directory, every one of them
// prints "bash.exe: warning: could not find /tmp, please create!". Anchor
// starts the first MSYS process itself, with the temp directory the process was
// started with, before the environment is moved.
package testiso

import (
	"sync"
	"sync/atomic"
	"time"
)

// anchorStopWait bounds how long stopAnchor waits for the shell to end once
// its stdin is closed; a shell that lingers past it is left to the process end.
const anchorStopWait = 5 * time.Second

var (
	anchorOnce sync.Once
	anchored   atomic.Bool
	// anchorRelease holds the anchor's stdin open: the shell lives until it
	// reads EOF there, which is the process ending or Release.
	anchorRelease = func() {}
	// anchorExited is closed once the anchor shell is gone.
	anchorExited <-chan struct{}
)

// anchor makes sure an MSYS process started under this process's own temp
// directory is alive for the rest of the process's life: it ends when the
// process does, however it ends. It does nothing off Windows or where no sh is
// on the PATH, and it never fails: a box without the shell tools has no /tmp to
// poison.
func anchor() {
	anchorOnce.Do(func() {
		anchorRelease, anchorExited = startAnchor()
		anchored.Store(true)
	})
}

// release lets the anchor go, for a caller that knows no run needs it any more.
func release() {
	anchorRelease()
}

// stopAnchor is release followed by a bounded wait for the shell to end, so
// the goroutine waiting on it is gone before the leak check. It returns at
// once when nothing was started.
func stopAnchor() {
	if !anchored.Load() {
		return
	}
	release()
	select {
	case <-anchorExited:
	case <-time.After(anchorStopWait):
	}
}
