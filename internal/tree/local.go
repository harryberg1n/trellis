package tree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/walls"
)

// mergeLocal lands the current tree in a repository with no remote, in the
// primary checkout: one squash commit on the default branch, or with rebase a
// --no-ff merge. Both are "Merge tree <name>" with the branch's subjects as
// the body, which keeps the tree visible in history; the event it records
// names the commit, which is what the report reads a merge from.
func mergeLocal(env Env, w where, check string, rebase bool) error {
	repo := w.Repo
	name := repo.Branch
	if check != "" {
		if err := runCheck(env, repo.Root, name, check); err != nil {
			return err
		}
	}
	primary, err := objects.Open(w.Primary)
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	if primary.Branch == repo.Default && primary.Detached {
		return fmt.Errorf("tree merge: the primary checkout is in the middle of a rebase of %s; finish or abort it first", primary.Branch)
	}
	if primary.Branch != repo.Default {
		return fmt.Errorf("tree merge: the primary checkout is on %s, not %s", primary.Branch, repo.Default)
	}
	// Untracked files in the primary never block a landing: a merge does
	// not touch them, and git refuses on its own when one would be
	// overwritten. The primary of every installed repository holds at
	// least one, the settings file install wrote.
	dirty, err := walls.Dirty(env.git(), w.Primary, walls.IgnoreUntracked())
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	if dirty != "" {
		return fmt.Errorf("tree merge: the primary checkout has %s; commit or discard it first", dirty)
	}

	subject := "Merge tree " + name
	body := mergeBody(env, w.Primary, repo.Default, name)
	if rebase {
		args := []string{"merge", "--no-ff", "--no-edit", "-m", subject}
		if body != "" {
			args = append(args, "-m", body)
		}
		if _, err := env.runGit(w.Primary, append(args, name)...); err != nil {
			return abortMerge(env, w, err)
		}
	} else {
		// The tip goes in the body, so tree rm can tell this very branch from a
		// later one that reuses the name.
		tip, err := env.runGit(w.Primary, "rev-parse", "refs/heads/"+name)
		if err != nil {
			return fmt.Errorf("tree merge: %w", err)
		}
		if err := squashLocal(env, w, subject, strings.TrimSpace(body+"\n\ntip "+tip)); err != nil {
			return err
		}
	}
	// The change is committed: recording it and removing the tree finish even
	// if the verb is interrupted now.
	env = env.uncancelled()
	sha, err := env.runGit(w.Primary, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	env.record(localMergedEvent(name, sha))
	env.say("merged %s into %s at %s", name, repo.Default, short(sha))
	return removeTree(env, w, repo.Root, name, false)
}

// conflictFixable is the Fixable for a local merge that conflicted in files.
func conflictFixable(repo objects.Repo, files []string) error {
	where := "(files unknown)"
	if len(files) > 0 {
		where = "in: " + strings.Join(files, ", ")
	}
	return &Fixable{Msg: fmt.Sprintf("tree merge: conflicts with %s %s\nrun trellis tree sync in the tree, resolve, merge again", repo.Default, where)}
}

// abortMerge answers a failed --no-ff merge. A merge that stopped on
// conflicts leaves MERGE_HEAD: whatever else fails, the primary checkout must
// not be left merging.
func abortMerge(env Env, w where, cause error) error {
	cancelled := errors.Is(cause, context.Canceled)
	env = env.uncancelled() // the abort must run after an interrupt
	if _, herr := env.runGit(w.Primary, "rev-parse", "-q", "--verify", "MERGE_HEAD"); herr != nil {
		return fmt.Errorf("tree merge: %w", cause)
	}
	files := lines(mustGit(env, w.Primary, "diff", "--name-only", "--diff-filter=U"))
	if _, aerr := env.runGit(w.Primary, "merge", "--abort"); aerr != nil {
		return fmt.Errorf("tree merge: could not abort the merge in %s: %w", w.Primary, aerr)
	}
	if cancelled {
		return interruptedf("tree merge: interrupted; the merge in %s was aborted", w.Primary)
	}
	return conflictFixable(w.Repo, files)
}

// squashLocal stages the tree's changes in the primary with git merge
// --squash and commits them as one commit. A squash that conflicts leaves no
// MERGE_HEAD but a dirty index, so it is undone by hand (see undoSquash).
func squashLocal(env Env, w where, subject, body string) error {
	if _, err := env.runGit(w.Primary, "merge", "--squash", w.Repo.Branch); err != nil {
		return undoFailedSquash(env, w, err, true)
	}
	args := []string{"commit", "-m", subject}
	if body != "" {
		args = append(args, "-m", body)
	}
	if _, err := env.runGit(w.Primary, args...); err != nil {
		return undoFailedSquash(env, w, err, false)
	}
	return nil
}

// undoFailedSquash puts the primary right after a squash or its commit failed
// with cause. It runs to the end even when the verb was interrupted (that is
// often the cause): a primary left holding a staged squash is the worst
// outcome of a Ctrl-C. A conflict names its files only when it came from the
// squash (merging).
func undoFailedSquash(env Env, w where, cause error, merging bool) error {
	if merging && errors.Is(cause, run.ErrNotStarted) {
		return interruptedf("tree merge: interrupted before the squash started")
	}
	cancelled := errors.Is(cause, context.Canceled)
	env = env.uncancelled()
	var files []string
	if merging {
		files = lines(mustGit(env, w.Primary, "diff", "--name-only", "--diff-filter=U"))
	}
	// A lock left by the git child that our cancel killed is ours to remove, and
	// only then: on an ordinary failure a live git of someone else may hold it.
	if cancelled && !errors.Is(cause, run.ErrNotStarted) {
		if lerr := removeStaleIndexLock(env, w.Primary); lerr != nil {
			return fmt.Errorf("tree merge: could not undo the squash in %s: %s", w.Primary, firstLine(lerr))
		}
	}
	if uerr := undoSquash(env, w.Primary); uerr != nil {
		return fmt.Errorf("tree merge: could not undo the squash in %s: %s", w.Primary, firstLine(uerr))
	}
	switch {
	case cancelled:
		return interruptedf("tree merge: interrupted; the squash in %s was undone", w.Primary)
	case len(files) > 0:
		return conflictFixable(w.Repo, files)
	}
	return fmt.Errorf("tree merge: %s", firstLine(cause))
}

// undoSquash puts the primary back to HEAD after a squash that stopped. It
// touches only the paths the squash changed, read from the diff against HEAD:
// the primary was verified clean (untracked files aside) before the merge, so
// every tracked path that differs is the squash's, and no work of the
// user's is discarded. A path HEAD has is restored from HEAD; one it lacks
// was added by the squash and is removed. The paths are read NUL-separated, so
// a name git would quote is exact, and handed to git literally, so a name with
// a glob character matches only itself. The squash's leftovers (SQUASH_MSG,
// MERGE_MSG, AUTO_MERGE) go too, so the next commit does not open prefilled.
func undoSquash(env Env, dir string) error {
	out, err := env.runGit(dir, "diff", "-z", "--no-renames", "--name-only", "HEAD")
	if err != nil {
		return err
	}
	var have, added []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" {
			continue
		}
		if _, err := env.runGit(dir, "cat-file", "-e", "HEAD:"+p); err == nil {
			have = append(have, p)
		} else {
			added = append(added, p)
		}
	}
	if len(have) > 0 {
		args := append([]string{"--literal-pathspecs", "restore", "--source=HEAD", "--staged", "--worktree", "--"}, have...)
		if _, err := env.runGit(dir, args...); err != nil {
			return err
		}
	}
	if len(added) > 0 {
		args := append([]string{"--literal-pathspecs", "rm", "-f", "-q", "--"}, added...)
		if _, err := env.runGit(dir, args...); err != nil {
			return err
		}
	}
	if _, err := env.runGit(dir, "reset", "-q"); err != nil {
		return err
	}
	for _, name := range []string{"SQUASH_MSG", "MERGE_MSG", "AUTO_MERGE"} {
		p, err := env.runGit(dir, "rev-parse", "--git-path", name)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// removeStaleIndexLock deletes the index.lock a git child that our cancel killed
// left behind. The child has exited by now; restoring paths fails while the
// lock is there. Call it only after our own cancel, never for a failure whose
// lock may belong to someone else's git.
func removeStaleIndexLock(env Env, dir string) error {
	p, err := env.runGit(dir, "rev-parse", "--git-path", "index.lock")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// mergeBody is the first-parent subject lines of the commits name adds to
// base, one "- subject" each, oldest first.
func mergeBody(env Env, dir, base, name string) string {
	subjects := lines(mustGit(env, dir, "log", "--first-parent", "--reverse", "--format=%s", base+".."+name))
	for i, s := range subjects {
		subjects[i] = "- " + s
	}
	return strings.Join(subjects, "\n")
}
