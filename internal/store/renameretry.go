// Source: aphrollo-tools 12b3724e internal/tdd/core/renameretry.go.
// Incident: CHANGELOG 1.5.x "two Windows flakes in the gate's own state writes":
// a rename over a file another process has open fails on Windows, and a
// reader that polls leaves only short gaps, so the rename is tried again.

package store

import (
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
)

const (
	// renameBound is how long a rename that fails for a reason that can pass
	// is tried again before its error is returned.
	renameBound = time.Second
	// renameRetryEvery is the first pause between tries, doubled up to
	// renameRetryCap: a reader that polls in a tight loop leaves only short
	// gaps, which a slow poll rarely lands in.
	renameRetryEvery = time.Millisecond
	renameRetryCap   = 20 * time.Millisecond
)

// retryRename runs rename until it succeeds, fails with an error retryable
// rejects, or bound has passed on c, and answers the last error. The pauses
// are waits on c, never a sleep.
func retryRename(c clock.Clock, rename func() error, retryable func(error) bool, bound time.Duration) error {
	deadline := c.Now().Add(bound)
	pause := renameRetryEvery
	for {
		err := rename()
		if err == nil || !retryable(err) || !c.Now().Before(deadline) {
			return err
		}
		<-c.After(pause)
		pause = min(pause*2, renameRetryCap)
	}
}
