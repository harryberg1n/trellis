package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/hook"
	runner "github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/walls"
)

// hookVerb is `trellis hook <event>`: it reads one harness payload from stdin
// and records it. It exits 0 whatever happens inside; only a PreToolUse deny exits 2 and prints. A
// missing event is a wiring mistake and exits 1, which the harness shows to
// the user without blocking the agent.
func hookVerb(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	// Whatever panics below, the agent must not see a trace or a blocking exit.
	defer func() {
		if r := recover(); r != nil {
			code = 0
		}
	}()
	if len(args) != 1 {
		_, _ = io.WriteString(stderr, "usage: trellis hook <event>\n")
		return 1
	}
	raw, err := hook.ReadPayload(stdin)
	if err != nil {
		raw = nil // Run records the empty payload as an internal.error
	}
	clk := clock.Real()
	deps := hook.Deps{
		Runner: runner.Exec{Clock: clk},
		Clock:  clk,
		Rows:   walls.Rows(),
	}
	// In the user's cache dir (private to them, unlike a shared temp dir), not
	// under the state root: it is for when the state root is what failed.
	if cache, err := os.UserCacheDir(); err == nil {
		deps.Fallback = filepath.Join(cache, "trellis", "fallback")
		if os.MkdirAll(deps.Fallback, 0o700) != nil {
			deps.Fallback = ""
		}
	}
	repo, err := hook.Locate(hook.PayloadCwd(raw))
	if err == nil {
		deps.Repo = &repo
		deps.Store, err = storeOf(repo, clk)
	}
	deps.Err = err
	return hook.Run(args[0], bytes.NewReader(raw), stdout, stderr, deps)
}
