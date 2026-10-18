package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/hook"
	"github.com/harryberg1n/trellis/internal/kernel"
	runner "github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/store"
)

// hookVerb is `trellis hook <event>`: it reads one harness payload from stdin
// and records it. It exits 0 whatever happens inside, and prints nothing. A
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
		Rows:   kernel.Table,
		// Under the OS temp dir, not the state root: it is for when the state
		// root is what failed.
		Fallback: filepath.Join(os.TempDir(), "trellis-fallback"),
	}
	if repo, err := hook.Locate(hook.PayloadCwd(raw)); err != nil {
		deps.Err = err
	} else if root, err := store.Root(repo.Root); err != nil {
		deps.Err = err
	} else {
		deps.Store, deps.Err = store.Open(store.RepoDir(root, store.RepoID(repo.Remote, repo.Root)), clk)
	}
	return hook.Run(args[0], bytes.NewReader(raw), stdout, stderr, deps)
}
