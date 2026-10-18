// Package tree is the four verbs of `trellis tree`: new, sync, merge and rm.
// A tree is a git worktree on a branch of the same name. The verbs are plain
// git and gh underneath; what a person wants kept, the events, they leave in
// the repository's store through Env.Record.
package tree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/host"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/store"
	"github.com/harryberg1n/trellis/internal/walls"
)

// checkTimeout bounds the --check command.
const checkTimeout = 30 * time.Minute

// Env is what a verb runs against.
type Env struct {
	Dir    string     // the working directory the verb was started in
	Runner run.Runner // starts every child; the verbs mark their git children Direct
	Out    io.Writer  // one line per step
	Err    io.Writer  // warnings; nil discards them
	Record func(kernel.Event) error
	Clock  clock.Clock // paces the mergeable poll; nil is the wall clock
	// Leave is called with the primary checkout before a tree the process
	// stands in is removed, so the process can step out of it. May be nil.
	Leave func(primary string) error
	// Ctx, when cancelled, ends the running child and its tree and stops the
	// verb; nil is never cancelled. A local squash in flight is undone first.
	Ctx context.Context
}

func (e Env) git() run.Runner { return run.Direct{Inner: e.runner()} }

// runner is the env's runner with the env's context given to every child.
func (e Env) runner() run.Runner { return run.WithContext(e.Runner, e.Ctx) }

// uncancelled is e with a context that is never cancelled: the work that
// puts a half-done change right again must run after an interrupt.
func (e Env) uncancelled() Env {
	if e.Ctx != nil {
		e.Ctx = context.WithoutCancel(e.Ctx)
	}
	return e
}

// cancelled reports whether the verb was interrupted.
func (e Env) cancelled() bool { return e.Ctx != nil && e.Ctx.Err() != nil }

// interrupted is err, or when the verb was interrupted and err does not say so
// already, a one-line error that does.
func (e Env) interrupted(verb string, err error) error {
	if err == nil || !e.cancelled() || strings.Contains(err.Error(), "interrupted") {
		return err
	}
	return fmt.Errorf("tree %s: interrupted: %w", verb, context.Canceled)
}

// wait pauses for d on the env's clock.
func (e Env) wait(d time.Duration) {
	clk := e.Clock
	if clk == nil {
		clk = clock.Real()
	}
	var done <-chan struct{}
	if e.Ctx != nil {
		done = e.Ctx.Done()
	}
	select {
	case <-clk.After(d):
	case <-done:
	}
}

// Fixable is a failure the operator can fix and run the verb again: red CI, a
// conflict, a branch that is behind, a head that moved. The command exits 2
// for it and 1 for any other error.
type Fixable struct{ Msg string }

func (f *Fixable) Error() string { return f.Msg }

func (e Env) say(format string, a ...any) { _, _ = fmt.Fprintf(e.Out, format+"\n", a...) }

func (e Env) warn(format string, a ...any) {
	if e.Err != nil {
		_, _ = fmt.Fprintf(e.Err, "trellis: "+format+"\n", a...)
	}
}

// record leaves ev in the store. A store that cannot be written is a warning:
// the git work it describes is done.
func (e Env) record(ev kernel.Event) {
	if e.Record == nil {
		return
	}
	if err := e.Record(ev); err != nil {
		e.warn("could not record %s: %v", ev.Kind, err)
	}
}

// runGit runs git in dir and returns its trimmed standard output.
func (e Env) runGit(dir string, args ...string) (string, error) {
	out, err := objects.Git(e.git(), dir, nil, args...)
	return strings.TrimSpace(string(out)), err
}

// where is the tree a verb was started in.
type where struct {
	Repo    objects.Repo
	Primary string // the primary checkout
}

func locate(dir string) (where, error) {
	if !filepath.IsAbs(dir) {
		return where{}, fmt.Errorf("%q is not absolute", dir)
	}
	repo, err := objects.Open(dir)
	if err != nil {
		return where{}, err
	}
	w := where{Repo: repo, Primary: repo.Root}
	if !repo.Primary {
		w.Primary = filepath.Dir(repo.Common)
	}
	return w, nil
}

// Path is where a tree called name lives by default: a sibling of the
// primary checkout, "<checkout>--<name>", slashes in the name becoming "--".
func Path(primary, name string) string {
	return primary + "--" + strings.ReplaceAll(name, "/", "--")
}

func (e Env) branchExists(dir, name string) (bool, error) {
	out, err := e.runGit(dir, "for-each-ref", "--format=%(refname)", "refs/heads/"+name)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Fields(out) {
		if line == "refs/heads/"+name {
			return true, nil
		}
	}
	return false, nil
}

// New opens a tree called name on a new branch of that name, cut from
// origin's default branch, at path (Path when empty).
func New(env Env, name, path string) error {
	return env.interrupted("new", newTree(env, name, path))
}

func newTree(env Env, name, path string) error {
	if name == "" {
		return errors.New("tree new: a name is needed")
	}
	if strings.Trim(name, "0123456789") == "" {
		return errors.New("tree new: a tree name cannot be only digits")
	}
	w, err := env.locate("new")
	if err != nil {
		return err
	}
	switch {
	case path == "":
		path = Path(w.Primary, name)
	case !filepath.IsAbs(path):
		path = filepath.Join(env.Dir, path)
	}
	if found, err := env.branchExists(w.Primary, name); err != nil {
		return fmt.Errorf("tree new: %w", err)
	} else if found {
		return fmt.Errorf("tree new: branch %q exists", name)
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("tree new: path %s exists", path)
	}
	if w.Repo.Remote != "" {
		if _, err := env.runGit(w.Primary, "fetch", "origin"); err != nil {
			return fmt.Errorf("tree new: %w", err)
		}
	}
	if _, err := env.runGit(w.Primary, "worktree", "add", "-b", name, path, baseOf(w.Repo)); err != nil {
		return fmt.Errorf("tree new: %w", err)
	}
	env.record(openedEvent(name))
	env.say("%s", path)
	return nil
}

// baseOf is the ref a tree is cut from and rebased onto: origin's default
// branch, or the local one in a repository with no remote.
func baseOf(repo objects.Repo) string {
	if repo.Remote == "" {
		return repo.Default
	}
	return "origin/" + repo.Default
}

// Sync fetches origin and rebases the current tree onto its default branch.
// A conflict leaves the rebase where git stopped it.
func Sync(env Env) error { return env.interrupted("sync", syncTree(env)) }

func syncTree(env Env) error {
	w, err := env.locate("sync")
	if err != nil {
		return err
	}
	if w.Repo.Remote != "" {
		if _, err := env.runGit(w.Repo.Root, "fetch", "origin"); err != nil {
			return fmt.Errorf("tree sync: %w", err)
		}
	}
	base := baseOf(w.Repo)
	if out, err := env.runGit(w.Repo.Root, "rebase", base); err != nil {
		return syncError(env, w.Repo, out, err)
	}
	env.say("rebased %s onto %s", w.Repo.Branch, base)
	return nil
}

// MergeOption changes how Merge lands a tree.
type MergeOption func(*mergeOpts)

type mergeOpts struct{ rebase bool }

// Rebase lands the tree's commits one by one (a rebase merge, or in a
// repository with no remote a --no-ff merge) instead of as one squash commit.
func Rebase() MergeOption { return func(o *mergeOpts) { o.rebase = true } }

// Merge ships the current tree: the optional local check, a push, a pull
// request (found or opened), its checks, a squash merge (a rebase merge with
// Rebase), then the tree and branch are removed. Any step that fails stops
// the rest.
func Merge(env Env, check string, opts ...MergeOption) error {
	return env.interrupted("merge", mergeTree(env, check, opts...))
}

func mergeTree(env Env, check string, opts ...MergeOption) error {
	var o mergeOpts
	for _, opt := range opts {
		opt(&o)
	}
	w, err := env.locate("merge")
	if err != nil {
		return err
	}
	repo := w.Repo
	switch {
	case repo.Branch == repo.Default:
		return fmt.Errorf("tree merge: %s is the default branch", repo.Branch)
	case repo.Primary:
		return errors.New("tree merge: the primary checkout is not a tree")
	case repo.Detached:
		return errors.New("tree merge: HEAD is detached")
	}
	name := repo.Branch
	if err := refuseDirty(env, repo.Root, name); err != nil {
		return err
	}
	if repo.Remote == "" {
		return mergeLocal(env, w, check, o.rebase)
	}
	if check != "" {
		if err := runCheck(env, repo.Root, name, check); err != nil {
			return err
		}
	}
	if err := (host.GH{Runner: env.runner(), Dir: repo.Root}).Ready(); err != nil {
		return errors.New("tree merge: gh is not installed or not authenticated")
	}
	if _, err := env.runGit(repo.Root, "push", "--force-with-lease", "-u", "origin", name); err != nil {
		return fmt.Errorf("tree merge: push failed: %s", firstLine(err))
	}
	env.say("pushed %s", name)
	gh := host.GH{Runner: env.runner(), Dir: repo.Root}
	if err := ensurePullRequest(gh, name, repo.Default); err != nil {
		return fmt.Errorf("tree merge: could not open the PR: %s", firstLine(err))
	}
	st, err := gh.ViewMergeable(name, env.wait)
	if err != nil {
		return fmt.Errorf("tree merge: could not open the PR: %s", firstLine(err))
	}
	pr := st.Number
	env.say("pull request #%d", pr)
	switch {
	case st.IsConflicting():
		return conflictError(env, repo)
	case st.IsBehind():
		return &Fixable{Msg: "tree merge: behind origin/" + repo.Default + ": run trellis tree sync"}
	case st.MergeUnknown():
		return &Fixable{Msg: "tree merge: mergeability not computed yet by GitHub: run again"}
	}
	seen, err := awaitChecks(env, gh, pr, name)
	if err != nil {
		return err
	}
	if !seen {
		env.say("no checks on this PR")
	}
	if err := watchAndReady(env, gh, repo, name, pr, seen); err != nil {
		return err
	}
	method := host.MergeSquash
	if o.rebase {
		method = host.MergeRebase
	}
	if err := gh.MergePR(pr, method); err != nil {
		// gh merges first and then tries to move this checkout off the branch,
		// which it cannot while the default branch is checked out elsewhere.
		if st, verr := gh.PRView(name); verr != nil || st.State != "MERGED" {
			return fmt.Errorf("tree merge: merge failed: %s", firstLine(err))
		}
	}
	sha, err := gh.MergeCommit(pr)
	if err != nil {
		env.warn("merge commit of #%d not read: %s", pr, firstLine(err))
	} else if sha == "" {
		env.warn("merge commit not reported yet; the report links this tree by subject")
	}
	env.record(mergedEvent(name, pr, sha))
	env.say("merged #%d", pr)
	return removeTree(env, w, repo.Root, name, false)
}

// runCheck runs cmd in dir, records its verdict, and fails on red.
func runCheck(env Env, dir, name, cmd string) error {
	prog, args := shellCommand(cmd)
	res, err := env.runner().Light(run.Spec{Name: prog, Args: args, Dir: dir, Timeout: checkTimeout})
	if err != nil {
		env.record(checkEvent(name, "red"))
		return &Fixable{Msg: fmt.Sprintf("tree merge: check %q failed: %s", cmd, lastLine(res, err))}
	}
	env.record(checkEvent(name, "green"))
	env.say("check passed")
	// a check that writes artifacts must not send the merge on: the push would
	// leave them out and the removal after the merge would refuse the tree
	dirty, err := walls.Dirty(env.git(), dir)
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	if dirty != "" {
		return fmt.Errorf("tree merge: the check left %s in %s; commit, ignore or clean them first", dirty, name)
	}
	return nil
}

func lastLine(res run.Result, err error) string {
	for _, b := range [][]byte{res.Stderr, res.Stdout} {
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		if l := plain(strings.TrimSpace(lines[len(lines)-1])); l != "" {
			return l
		}
	}
	line, _, _ := strings.Cut(err.Error(), "\n")
	return plain(line)
}

// ensurePullRequest opens a pull request of branch against base unless an
// open one exists.
func ensurePullRequest(gh host.GH, branch, base string) error {
	st, err := gh.PRView(branch)
	if err == nil && st.State == "OPEN" {
		return nil
	}
	if err != nil && !errors.Is(err, host.ErrNoPR) {
		return err
	}
	_, err = gh.CreatePR(base)
	return err
}

// readyToMerge reads the pull request once more after the checks and is a
// Fixable unless gh would merge it: the head must still be the commit this
// tree is on (the checks judged it), and the merge state must be clean. A
// state that is not clean only because a check is still pending is no verdict:
// pending is true and the error nil.
func readyToMerge(env Env, gh host.GH, repo objects.Repo, branch string) (pending bool, err error) {
	st, err := gh.PRView(branch)
	if err != nil {
		return false, fmt.Errorf("tree merge: %w", err)
	}
	local, err := env.runGit(repo.Root, "rev-parse", "HEAD")
	if err != nil {
		return false, fmt.Errorf("tree merge: %w", err)
	}
	notClean := strings.EqualFold(st.MergeStateStatus, "BLOCKED") || strings.EqualFold(st.MergeStateStatus, "UNSTABLE")
	if notClean && anyPending(gh, st.Number) {
		return true, nil
	}
	switch {
	case st.HeadSHA != "" && !strings.EqualFold(st.HeadSHA, local):
		return false, &Fixable{Msg: fmt.Sprintf("tree merge: head moved: local %s vs PR %s", short(local), short(st.HeadSHA))}
	case st.IsConflicting():
		return false, conflictError(env, repo)
	case st.IsBehind():
		return false, &Fixable{Msg: "tree merge: behind origin/" + repo.Default + ": run trellis tree sync"}
	case strings.EqualFold(st.MergeStateStatus, "BLOCKED"):
		return false, &Fixable{Msg: "tree merge: blocked by branch protection" + blockReason(st.ReviewDecision)}
	case strings.EqualFold(st.MergeStateStatus, "UNSTABLE"):
		// the watch said green, so this is our read being wrong: red all the same
		return false, unpassedChecks(env, gh, st.Number, "merge state UNSTABLE after a green watch")
	}
	return false, nil
}

// blockReason is ": <why>" for a review decision gh names, else "".
func blockReason(reviewDecision string) string {
	switch strings.ToUpper(reviewDecision) {
	case "REVIEW_REQUIRED":
		return ": review required"
	case "CHANGES_REQUESTED":
		return ": changes requested"
	}
	return ""
}

// refuseDirty is a one-line refusal when the tree holds work no commit does:
// a push would leave it out and the removal after the merge would refuse it.
func refuseDirty(env Env, dir, name string) error {
	dirty, err := walls.Dirty(env.git(), dir)
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	if dirty != "" {
		return fmt.Errorf("tree merge: %s has %s; commit or discard it first", name, dirty)
	}
	return nil
}

func short(sha string) string { return sha[:min(12, len(sha))] }

// Rm removes the tree called name (the current tree when empty) and its
// branch. A tree with work no commit holds is refused unless force.
func Rm(env Env, name string, force bool) error {
	return env.interrupted("rm", rmTree(env, name, force))
}

func rmTree(env Env, name string, force bool) error {
	w, err := env.locate("rm")
	if err != nil {
		return err
	}
	path := w.Repo.Root
	if name == "" {
		if w.Repo.Primary {
			return errors.New("tree rm: this is the primary checkout, not a tree")
		}
		name = w.Repo.Branch
	} else if path, err = pathOf(env, w, name); err != nil {
		return fmt.Errorf("tree rm: %w", err)
	}
	if path == w.Primary {
		return errors.New("tree rm: that is the primary checkout, not a tree")
	}
	if !force {
		dirty, err := walls.Dirty(env.git(), path)
		if err != nil {
			return fmt.Errorf("tree rm: %w", err)
		}
		if dirty != "" {
			return fmt.Errorf("tree rm: %s has %s; --force removes it anyway", name, dirty)
		}
		if err := refuseUnmerged(env, w, name); err != nil {
			return err
		}
	}
	return removeTree(env, w, path, name, force)
}

// refuseUnmerged is a refusal when the branch holds commits that are neither
// in the default branch (origin's, or the local one without a remote) nor in
// its upstream: deleting the branch would lose them.
func refuseUnmerged(env Env, w where, name string) error {
	if found, err := env.branchExists(w.Primary, name); err != nil {
		return fmt.Errorf("tree rm: %w", err)
	} else if !found {
		return nil
	}
	base := baseOf(w.Repo)
	n, err := env.countCommits(w.Primary, base+".."+name)
	if err != nil {
		return fmt.Errorf("tree rm: %w", err)
	}
	if n == 0 {
		return nil
	}
	if _, uerr := env.runGit(w.Primary, "rev-parse", "--verify", "-q", name+"@{upstream}"); uerr == nil {
		if ahead, err := env.countCommits(w.Primary, name+"@{upstream}.."+name); err == nil && ahead == 0 {
			return nil
		}
	}
	noun := "commits"
	if n == 1 {
		noun = "commit"
	}
	return fmt.Errorf("tree rm: %s has %d %s not merged into %s; --force removes it anyway", name, n, noun, base)
}

// countCommits is the number of commits in a git revision range.
func (e Env) countCommits(dir, rng string) (int, error) {
	out, err := e.runGit(dir, "rev-list", "--count", rng)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// pathOf is the path of the worktree on branch name.
func pathOf(env Env, w where, name string) (string, error) {
	out, err := env.runGit(w.Primary, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	var path string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = store.Canonical(filepath.FromSlash(strings.TrimPrefix(line, "worktree ")))
		case line == "branch refs/heads/"+name:
			return path, nil
		}
	}
	return "", fmt.Errorf("no tree is on branch %q", name)
}

// removeTree removes the worktree at path and the branch name, from the
// primary checkout. A branch gh already deleted is not an error.
func removeTree(env Env, w where, path, name string, force bool) error {
	if path == w.Repo.Root && env.Leave != nil {
		if err := env.Leave(w.Primary); err != nil {
			return fmt.Errorf("tree rm: leaving %s: %w", path, err)
		}
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	if _, err := env.runGit(w.Primary, append(args, path)...); err != nil {
		return fmt.Errorf("tree rm: %w", err)
	}
	if found, err := env.branchExists(w.Primary, name); err != nil {
		return fmt.Errorf("tree rm: %w", err)
	} else if found {
		if _, err := env.runGit(w.Primary, "branch", "-D", name); err != nil {
			return fmt.Errorf("tree rm: %w", err)
		}
	}
	env.say("removed %s", path)
	return nil
}

// locate is locate(e.Dir) with the remote read the way git reads it. The
// repository description of objects.Open is read from files and sees only
// the repository's own config; here git's config answers, includes and all.
// A verb must not guess its base: with remotes but none called origin, or an
// origin whose default branch git has not been told, it stops with a line.
func (e Env) locate(verb string) (where, error) {
	w, err := locate(e.Dir)
	if err != nil {
		return where{}, fmt.Errorf("tree %s: %w", verb, err)
	}
	url, err := e.runGit(w.Repo.Root, "config", "--get", "remote.origin.url")
	if err != nil || url == "" {
		remotes, rerr := e.runGit(w.Repo.Root, "remote")
		if rerr != nil {
			return where{}, fmt.Errorf("tree %s: %w", verb, rerr)
		}
		if names := strings.Fields(remotes); len(names) > 0 {
			slices.Sort(names)
			return where{}, fmt.Errorf("tree %s: no remote named origin (remotes: %s)", verb, strings.Join(names, ", "))
		}
		w.Repo.Remote = ""
		return w, nil
	}
	w.Repo.Remote = url
	head, err := e.runGit(w.Repo.Root, "symbolic-ref", "-q", "refs/remotes/origin/HEAD")
	def, ok := strings.CutPrefix(head, "refs/remotes/origin/")
	if err != nil || !ok || def == "" {
		return where{}, fmt.Errorf("tree %s: origin/HEAD is not set: run git remote set-head origin -a", verb)
	}
	w.Repo.Default = def
	return w, nil
}
