// Package tree is the four verbs of `trellis tree`: new, sync, merge and rm.
// A tree is a git worktree on a branch of the same name. The verbs are plain
// git and gh underneath; what a person wants kept, the events, they leave in
// the repository's store through Env.Record.
package tree

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
}

func (e Env) git() run.Runner { return run.Direct{Inner: e.Runner} }

// wait pauses for d on the env's clock.
func (e Env) wait(d time.Duration) {
	clk := e.Clock
	if clk == nil {
		clk = clock.Real()
	}
	<-clk.After(d)
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
	if name == "" {
		return errors.New("tree new: a name is needed")
	}
	w, err := locate(env.Dir)
	if err != nil {
		return fmt.Errorf("tree new: %w", err)
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
func Sync(env Env) error {
	w, err := locate(env.Dir)
	if err != nil {
		return fmt.Errorf("tree sync: %w", err)
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

// Merge ships the current tree: the optional local check, a push, a pull
// request (found or opened), its checks, a rebase merge, then the tree and
// branch are removed. Any step that fails stops the rest.
func Merge(env Env, check string) error {
	w, err := locate(env.Dir)
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	repo := w.Repo
	switch {
	case repo.Branch == repo.Default:
		return fmt.Errorf("tree merge: %s is the default branch", repo.Branch)
	case repo.Primary:
		return errors.New("tree merge: the primary checkout is not a tree")
	case strings.HasPrefix(repo.Branch, "detached-"):
		return errors.New("tree merge: HEAD is detached")
	}
	name := repo.Branch
	if err := refuseDirty(env, repo.Root, name); err != nil {
		return err
	}
	if repo.Remote == "" {
		return mergeLocal(env, w, check)
	}
	if check != "" {
		if err := runCheck(env, repo.Root, name, check); err != nil {
			return err
		}
	}
	if err := (host.GH{Runner: env.Runner, Dir: repo.Root}).Ready(); err != nil {
		return errors.New("tree merge: gh is not installed or not authenticated")
	}
	if _, err := env.runGit(repo.Root, "push", "--force-with-lease", "-u", "origin", name); err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	env.say("pushed %s", name)
	gh := host.GH{Runner: env.Runner, Dir: repo.Root}
	if err := ensurePullRequest(gh, name, repo.Default); err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	st, err := gh.ViewMergeable(name, env.wait)
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
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
	switch _, err := gh.WatchChecks(pr); {
	case errors.Is(err, host.ErrNoChecks):
		env.say("no checks on this PR")
	case err != nil:
		return checksError(env, gh, pr, err)
	default:
		env.say("checks passed on #%d", pr)
	}
	if err := readyToMerge(env, gh, repo, name); err != nil {
		return err
	}
	if err := gh.MergePR(pr); err != nil {
		// gh merges first and then tries to move this checkout off the branch,
		// which it cannot while the default branch is checked out elsewhere.
		if st, verr := gh.PRView(name); verr != nil || st.State != "MERGED" {
			return fmt.Errorf("tree merge: #%d: %w", pr, err)
		}
	}
	env.record(mergedEvent(name, pr))
	env.say("merged #%d", pr)
	return removeTree(env, w, repo.Root, name, false)
}

// runCheck runs cmd in dir, records its verdict, and fails on red.
func runCheck(env Env, dir, name, cmd string) error {
	prog, args := shellCommand(cmd)
	res, err := env.Runner.Light(run.Spec{Name: prog, Args: args, Dir: dir, Timeout: checkTimeout})
	if err != nil {
		env.record(checkEvent(name, "red"))
		return &Fixable{Msg: fmt.Sprintf("tree merge: check %q failed: %s", cmd, lastLine(res, err))}
	}
	env.record(checkEvent(name, "green"))
	env.say("check passed")
	return nil
}

func lastLine(res run.Result, err error) string {
	for _, b := range [][]byte{res.Stderr, res.Stdout} {
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
			return l
		}
	}
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
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
// tree is on (the checks judged it), and the merge state must be clean.
func readyToMerge(env Env, gh host.GH, repo objects.Repo, branch string) error {
	st, err := gh.PRView(branch)
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	local, err := env.runGit(repo.Root, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("tree merge: %w", err)
	}
	switch {
	case st.HeadSHA != "" && !strings.EqualFold(st.HeadSHA, local):
		return &Fixable{Msg: fmt.Sprintf("tree merge: head moved: local %s vs PR %s", short(local), short(st.HeadSHA))}
	case st.IsConflicting():
		return conflictError(env, repo)
	case st.IsBehind():
		return &Fixable{Msg: "tree merge: behind origin/" + repo.Default + ": run trellis tree sync"}
	case strings.EqualFold(st.MergeStateStatus, "BLOCKED"):
		return &Fixable{Msg: "tree merge: blocked by branch protection" + blockReason(st.ReviewDecision)}
	case strings.EqualFold(st.MergeStateStatus, "UNSTABLE"):
		// the watch said green, so this is our read being wrong: red all the same
		return unpassedChecks(env, gh, st.Number, "merge state UNSTABLE after a green watch")
	}
	return nil
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
	w, err := locate(env.Dir)
	if err != nil {
		return fmt.Errorf("tree rm: %w", err)
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
	}
	return removeTree(env, w, path, name, force)
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
