// Package testsupport holds the one test helper that needs run and clock: a
// spawn-counting runner. The rest (git, files, fake tools) is testiso's, which
// run's own tests can import. Production code does not import it.
package testsupport

import (
	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/run"
)

// Recorder is a Runner over the real clock that counts what it spawns.
func Recorder() *run.Recorder {
	return &run.Recorder{Inner: run.Exec{Clock: clock.Real()}}
}
