package main

import (
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/walls"
)

// allowVerb is `trellis allow <row-id>`: it leaves a one-shot token that waives
// the next deny of that row in this repository. The hook consumes it and logs
// wall.overridden. Rows with no override (secrets, undercover, own-git-state)
// are refused. The token lasts store.AllowTTL (10 minutes) and is keyed by the
// repo id: a repo with no origin remote is keyed by its worktree path, so a
// token minted in the primary checkout is not seen from a lane.
func allowVerb(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = io.WriteString(stderr, "usage: trellis allow <row-id>\n")
		return 1
	}
	id := args[0]
	if !slices.ContainsFunc(kernel.Table, func(r kernel.Row) bool { return r.ID == id }) {
		_, _ = fmt.Fprintf(stderr, "trellis: unknown row %q\n", id)
		return 1
	}
	if !walls.Overridable(id) {
		_, _ = fmt.Fprintf(stderr, "trellis: %s has no override\n", id)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis: %v\n", err)
		return 1
	}
	s, err := openStore(cwd)
	if err == nil {
		err = s.Allow(id)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis: allow %s: %v\n", id, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "trellis: %s allowed once\n", id)
	return 0
}

// openStore is the event store of the repository around dir.
func openStore(dir string) (*store.Store, error) {
	repo, err := objects.Open(dir)
	if err != nil {
		return nil, err
	}
	return storeOf(repo, clock.Real())
}

// storeOf opens the store of an already located repository, under the state
// root for it.
func storeOf(repo objects.Repo, clk clock.Clock) (*store.Store, error) {
	root, err := store.Root(repo.Root)
	if err != nil {
		return nil, err
	}
	return storeIn(root, repo, clk)
}

// storeIn opens repo's store under the given state root. Hook, allow and
// report all arrive here, so they open the same directory.
func storeIn(stateRoot string, repo objects.Repo, clk clock.Clock) (*store.Store, error) {
	return store.Open(store.RepoDir(stateRoot, store.RepoID(repo.Remote, repo.Root)), clk)
}
