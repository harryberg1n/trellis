package tree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/objects"
)

// droppedRefs is where `tree drop` keeps the work of a tree it removes.
const droppedRefs = "refs/trellis/dropped/"

// Drop removes the tree called name (the current tree when empty) and its
// branch, and keeps everything it held under refs/trellis/dropped/: the
// branch tip, and the uncommitted work (untracked files included) as one
// commit on top of it. Owner, 2026-11-04: an agent could not drop a worthless
// lane that was not merged. `git branch -D` and `git worktree remove --force`
// meet discard-uncommitted, whose override is for a person, and
// `tree rm --force` discards for good. A drop loses nothing, so it needs no
// override; reason is recorded with it and said back by the report.
func Drop(env Env, name, reason string) error {
	return env.interrupted("drop", dropTree(env, name, reason))
}

func dropTree(env Env, name, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("tree drop: --reason is needed: say why the work is dropped")
	}
	w, err := env.locate("drop")
	if err != nil {
		return err
	}
	path := w.Repo.Root
	if name == "" {
		if w.Repo.Primary {
			return errors.New("tree drop: the primary checkout is not a tree: name one, trellis tree drop <name> --reason <why>")
		}
		name = w.Repo.Branch
	} else if path, err = pathOf(env, w, name); err != nil {
		return fmt.Errorf("tree drop: %w", err)
	}
	if path == w.Primary {
		return errors.New("tree drop: that is the primary checkout, not a tree")
	}
	tip, err := keepWork(env, path, name)
	if err != nil {
		return fmt.Errorf("tree drop: %w", err)
	}
	ref, err := keepRef(env, w.Primary, name, tip)
	if err != nil {
		return fmt.Errorf("tree drop: %w", err)
	}
	env.record(droppedEvent(name, reason, ref))
	env.say("dropped %s: kept at %s (%s)", name, ref, short(tip))
	env.say("restore: git worktree add %s -b %s %s", path, name, ref)
	return removeTree(env, w, path, name, true)
}

// keepWork is the commit that holds the tree at path as it is: its HEAD when
// it is clean, else a commit of its working tree on top of HEAD, built from
// objects only (the index and the working tree are left alone).
func keepWork(env Env, path, name string) (string, error) {
	head, err := env.runGit(path, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	tree, err := objects.Snapshot(env.git(), path, nil)
	if err != nil {
		return "", err
	}
	headTree, err := env.runGit(path, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	if tree == headTree {
		return head, nil
	}
	return env.runGit(path, "-c", "user.name=trellis", "-c", "user.email=trellis@localhost", "-c", "commit.gpgsign=false",
		"commit-tree", tree, "-p", head, "-m", "trellis drop: uncommitted work of "+name)
}

// keepRef points a new ref under refs/trellis/dropped/ at tip and returns it:
// <name with / as -->-<UTC time>, and -2, -3, ... when that one exists.
func keepRef(env Env, primary, name, tip string) (string, error) {
	clk := env.Clock
	if clk == nil {
		clk = clock.Real()
	}
	base := droppedRefs + strings.ReplaceAll(name, "/", "--") + "-" + clk.Now().UTC().Format("20060102T150405Z")
	for n := 1; ; n++ {
		ref := base
		if n > 1 {
			ref += "-" + strconv.Itoa(n)
		}
		if _, err := env.runGit(primary, "rev-parse", "--verify", "-q", ref); err == nil {
			continue
		}
		// the zero old value: create only, never move a ref another drop made
		if _, err := env.runGit(primary, "update-ref", ref, tip, strings.Repeat("0", len(tip))); err != nil {
			return "", err
		}
		return ref, nil
	}
}

// Adopt makes a tree of a branch that exists already: on origin (tracking
// it), or in a repository with no remote, locally. Issue #52: a peer session
// pushed fix/worker-guards and asked this host to sync and merge it; `tree new`
// refuses a name already on origin, so the tree was made by hand.
func Adopt(env Env, name, path string) error {
	return env.interrupted("adopt", adoptTree(env, name, path))
}

func adoptTree(env Env, name, path string) error {
	w, err := env.locate("adopt")
	if err != nil {
		return err
	}
	if name == w.Repo.Default {
		return fmt.Errorf("tree adopt: %s is the default branch", name)
	}
	if at, err := pathOf(env, w, name); err == nil {
		return fmt.Errorf("tree adopt: %s is already a tree at %s", name, at)
	}
	switch {
	case path == "":
		path = Path(w.Primary, name)
	case !filepath.IsAbs(path):
		path = filepath.Join(env.Dir, path)
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("tree adopt: path %s exists", path)
	}
	local, err := env.branchExists(w.Primary, name)
	if err != nil {
		return fmt.Errorf("tree adopt: %w", err)
	}
	args := []string{"worktree", "add", path, name}
	if w.Repo.Remote != "" {
		if _, err := env.runGit(w.Primary, "fetch", "--prune", "origin"); err != nil {
			return fmt.Errorf("tree adopt: %w", err)
		}
		_, rerr := env.runGit(w.Primary, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+name)
		switch {
		case rerr != nil && !local:
			return fmt.Errorf("tree adopt: no branch %q on origin", name)
		case rerr == nil && !local:
			args = []string{"worktree", "add", "--track", "-b", name, path, "origin/" + name}
		}
	} else if !local {
		return fmt.Errorf("tree adopt: no branch %q", name)
	}
	if _, err := env.runGit(w.Primary, args...); err != nil {
		return fmt.Errorf("tree adopt: %w", err)
	}
	env.record(openedEvent(name))
	env.say("%s", path)
	return nil
}
