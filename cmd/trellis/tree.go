package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
	runner "github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/tree"
)

const treeUsage = `usage: trellis tree <verb>

verbs:
  new <name> [--path P]   open a tree: a branch of that name from origin's default, in a sibling directory;
                          refuses a name git cannot use as a branch, and one that is already a branch on origin
  sync                    fetch origin and rebase this tree onto its default branch; refuses uncommitted changes
  merge [<name>] [--check CMD] [--rebase]
                          push, open or find the pull request, wait for its checks, squash-merge it (one commit
                          per PR, its number in the subject, the usual norm) bound to the head the checks judged,
                          delete the branch on origin, remove the tree; --rebase keeps the tree's commits instead;
                          the output of --check is shown as it is written; with <name> it merges that tree from
                          anywhere, the primary checkout included, so your shell is not left in the removed tree;
                          no wait for checks when no workflow of the tree can run on a pull request
                          (a repository with no remote: one squash commit "Merge tree <name>" on the default
                          branch, no gh; --rebase makes it a --no-ff merge)
  adopt <branch> [--path P]
                          make a tree of a branch that exists already (on origin, tracked; or a local one), in the
                          usual sibling directory, for sync, merge and the report
  rm [<name>] [--force]   remove a tree and its branch; refuses uncommitted work or commits not merged or pushed without --force
  drop [<name>] --reason R
                          remove a tree and its branch, and keep all it held (commits, and uncommitted work as one
                          commit on top) at refs/trellis/dropped/<name>-<time>; prints the line that restores it; the
                          reason is recorded; nothing is lost, so it needs no --force and no override
  list [--size]           every worktree of the repository (trellis-made or not): commits ahead, uncommitted files,
                          pull request, age of the last commit, what prune would do; and orphan directories beside
                          the primary that are no worktree; --size adds the size on disk
  prune [--dry] [--backup]
                          remove the trees whose pull request merged with their tip and that are clean, and forget
                          the ones whose directory is gone; --backup also removes every other tree after keeping its
                          work as drop does and pushing it to origin as backup/<name>; never the tree you are in;
                          orphan directories are named, never deleted; --dry prints the plan

the loop (merge pushes and opens the PR itself; --check binds the merge to the head your gate judged):
  trellis tree new fix/login                          # from the primary checkout
  git -C ../repo--fix--login commit -m 'fix: ...'    # work and commit in the tree it printed
  trellis tree merge fix/login --check 'make check'  # from the primary: check, push, PR, merge, clean up

exit codes of merge and sync: 0 done; 130 interrupted; 2 when you can fix it and run it again (red or cancelled
checks, a conflict, a branch that is behind or blocked, a head that moved after the checks judged it, a run of
the head still queued after 30 minutes, uncommitted changes (sync), commits on origin the tree lacks (a refused
push), an untracked file in the primary at a path the tree changes); 1 anything else.
`

// treeUsageLine is the usage line of a tree verb.
func treeUsageLine(verb string) string {
	switch verb {
	case "new":
		return "tree new <name> [--path P]"
	case "merge":
		return "tree merge [<name>] [--check CMD] [--rebase]"
	case "rm":
		return "tree rm [<name>] [--force]"
	case "drop":
		return "tree drop [<name>] --reason R"
	case "adopt":
		return "tree adopt <branch> [--path P]"
	case "list":
		return "tree list [--size]"
	case "prune":
		return "tree prune [--dry] [--backup]"
	}
	return "tree " + verb
}

// treeEnv is what the tree verbs take from the process.
type treeEnv struct {
	Dir    string
	Runner runner.Runner
	Clock  clock.Clock
	Leave  func(primary string) error
	Ctx    context.Context // cancelled by a signal; nil is never cancelled
}

// treeVerb runs `trellis tree <verb>` in the real process.
func treeVerb(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	dir, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis tree: %v\n", err)
		return 1
	}
	clk := clock.Real()
	return treeCmd(args, treeEnv{Dir: dir, Runner: runner.Exec{Clock: clk}, Clock: clk, Leave: os.Chdir, Ctx: ctx}, stdout, stderr)
}

// treeExit is the exit code for a failed tree verb: 2 for a failure the
// operator can fix and run again, 1 for any other.
func treeExit(err error) int {
	var fixable *tree.Fixable
	if errors.As(err, &fixable) {
		return 2
	}
	return 1
}

// treeCmd dispatches one tree verb and returns the exit code. An error is
// reported on stderr.
func treeCmd(args []string, env treeEnv, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, treeUsage)
		return 2
	}
	verb := args[0]
	if verb == "-h" || verb == "--help" {
		_, _ = io.WriteString(stdout, treeUsage)
		return 0
	}
	fs := newFlagSet("tree "+verb, treeUsageLine(verb), stderr)
	var path, check, reason string
	var force, rebase, size, dry, backup bool
	switch verb {
	case "new":
		fs.StringVar(&path, "path", "", "where to put the tree (default: a sibling of this checkout)")
	case "merge":
		fs.StringVar(&check, "check", "", "a command to run in the tree first; red stops the merge")
		fs.BoolVar(&rebase, "rebase", false, "keep the tree's commits (a rebase merge) instead of squashing them into one")
	case "rm":
		fs.BoolVar(&force, "force", false, "remove the tree though it holds uncommitted or unmerged work")
	case "drop":
		fs.StringVar(&reason, "reason", "", "why the work is dropped; recorded with it")
	case "adopt":
		fs.StringVar(&path, "path", "", "where to put the tree (default: a sibling of the primary checkout)")
	case "list":
		fs.BoolVar(&size, "size", false, "add each tree's size on disk")
	case "prune":
		fs.BoolVar(&dry, "dry", false, "print the plan and change nothing")
		fs.BoolVar(&backup, "backup", false, "also remove every other tree, after keeping its work and pushing it to origin as backup/<name>")
	case "sync":
	default:
		_, _ = fmt.Fprintf(stderr, "trellis tree: unknown verb %q\n\n%s", verb, treeUsage)
		return 2
	}
	positional, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return parseExit(err)
	}
	want := map[string][2]int{"new": {1, 1}, "sync": {0, 0}, "merge": {0, 1}, "rm": {0, 1}, "drop": {0, 1}, "adopt": {1, 1}, "list": {0, 0}, "prune": {0, 0}}[verb]
	if len(positional) < want[0] || len(positional) > want[1] {
		_, _ = fmt.Fprintf(stderr, "trellis tree %s: wrong number of arguments\n", verb)
		fs.Usage()
		return 2
	}
	var name string
	if len(positional) > 0 {
		name = positional[0]
	}
	if verb == "drop" && strings.TrimSpace(reason) == "" {
		_, _ = io.WriteString(stderr, "trellis tree drop: --reason is needed: say why the work is dropped\n")
		return 2
	}
	e := tree.Env{
		Dir:    env.Dir,
		Runner: env.Runner,
		Clock:  env.Clock,
		Out:    stdout,
		Err:    stderr,
		Record: treeRecorder(env.Dir),
		Leave:  env.Leave,
		Ctx:    env.Ctx,
	}
	switch verb {
	case "new":
		err = tree.New(e, name, path)
	case "sync":
		err = tree.Sync(e)
	case "merge":
		var opts []tree.MergeOption
		if rebase {
			opts = append(opts, tree.Rebase())
		}
		if name != "" {
			opts = append(opts, tree.Named(name))
		}
		err = tree.Merge(e, check, opts...)
	case "rm":
		err = tree.Rm(e, name, force)
	case "drop":
		err = tree.Drop(e, name, reason)
	case "adopt":
		err = tree.Adopt(e, name, path)
	case "list":
		err = tree.List(e, size)
	case "prune":
		err = tree.Prune(e, dry, backup)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis %v\n", err)
		return treeExit(err)
	}
	return 0
}

// parseInterspersed parses fs over args, allowing flags after the positional
// arguments (`tree new x --path P`), and returns the positionals.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// treeRecorder appends an event to the store of the repository around dir.
func treeRecorder(dir string) func(kernel.Event) error {
	return func(ev kernel.Event) error {
		s, err := openStore(dir)
		if err != nil {
			return err
		}
		ev.At = clock.Real().Now()
		_, err = s.Append(ev)
		return err
	}
}
