package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/hook"
	"github.com/harryberg1n/trellis/internal/objects"
	runner "github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/store"
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
	repo, err := objects.Open(hook.PayloadCwd(raw))
	// The fallback is for when the state root is what failed, so it sits in
	// the user's cache dir (private to them), beside the state root and not
	// inside it, one directory per repo so the next good run folds its own.
	// Nothing creates it until an event is parked.
	cache, cerr := os.UserCacheDir()
	if cerr == nil {
		id := "unscoped"
		if err == nil {
			id = store.RepoID(repo.Remote, repo.Root)
		}
		deps.Fallback = filepath.Join(cache, "trellis-fallback", id)
	}
	if err == nil {
		deps.Repo = &repo
		deps.Store, err = storeOf(repo, clk)
	}
	deps.Err = err
	return hook.Run(args[0], bytes.NewReader(raw), stdout, stderr, deps)
}
