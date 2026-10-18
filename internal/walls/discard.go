package walls

// The intent parsing in this file is copied from aphrollo-tools
// internal/cli/git_shim_discard.go at 12b3724e (discardIntent and the
// per-verb functions, splitDashDash, nonFlagOperands). Incidents: #650, a
// `git checkout -- <path>` that threw away a day of uncommitted work, and #857,
// the same through a Bash tool call that never met the git shim. What changed:
// the cost is measured with one `git status` (the source ran a diff and an
// ls-files, and a rev-list for a branch) through objects.Git and run.Light,
// and the refusal is the trellis deny line.

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/run"
)

// discardUncommitted is the discard-uncommitted wall. A form that can throw
// away work no commit holds is measured with one light process, for that
// command only, and denied when the measure is not zero. A command that is
// not one of the forms starts nothing. A measurement that fails or times out
// is not a deny (the walls fail open): unmeasured says so, and the call is let
// through with that line as a warning.
func discardUncommitted(a *analysis) (cause, unmeasured string) {
	for _, c := range a.gitCalls() {
		form, paths, ok := discardIntent(append([]string{c.Verb}, c.Args...))
		if !ok {
			continue
		}
		cause, unmeasured := measureDiscard(a, c, form, paths)
		if cause != "" {
			return cause, ""
		}
		if unmeasured != "" {
			return "", unmeasured
		}
	}
	return "", ""
}

// cost is what an invocation would destroy.
type cost struct {
	Unstaged, Staged, Untracked, Ignored, Commits int
}

func (c cost) zero() bool {
	return c.Unstaged == 0 && c.Staged == 0 && c.Untracked == 0 && c.Ignored == 0 && c.Commits == 0
}

func (c cost) String() string {
	var parts []string
	if c.Unstaged > 0 {
		parts = append(parts, plural(c.Unstaged, "unstaged file"))
	}
	if c.Staged > 0 {
		parts = append(parts, plural(c.Staged, "staged file"))
	}
	if c.Untracked > 0 {
		parts = append(parts, plural(c.Untracked, "untracked file"))
	}
	if c.Ignored > 0 {
		parts = append(parts, plural(c.Ignored, "ignored file"))
	}
	if c.Commits > 0 {
		parts = append(parts, plural(c.Commits, "commit")+" unreachable from any other ref")
	}
	return strings.Join(parts, ", ")
}

// measureDiscard is the deny cause for a classified discard, or "" when it
// would destroy nothing (or the repository is not one we can read).
func measureDiscard(a *analysis, c gitCall, form string, paths []string) (cause, unmeasured string) {
	if a.r == nil {
		return "", ""
	}
	if _, ok := repoAt(c.Dir); !ok && form != "worktree remove --force" {
		return "", ""
	}
	switch {
	case strings.HasPrefix(form, "stash "):
		return form + " discards a stash entry for good", ""
	case strings.HasPrefix(form, "branch -D "):
		return measureBranch(a, c, form), ""
	}
	dir, where := c.Dir, ""
	if form == "worktree remove --force" {
		if len(paths) == 0 {
			return "", ""
		}
		dir = resolve(c.Dir, paths[0])
		if dir == "" {
			return "", ""
		}
		if _, err := os.Stat(dir); err != nil {
			return "", ""
		}
		where, paths = " in "+dir, nil
	}
	if dir == "" {
		return "", ""
	}
	cleans, only := cleanReach(form)
	got, err := statusCost(a.r, dir, paths, cleans)
	if err != nil {
		return "", fmt.Sprintf("could not measure what %s would discard (%s); allowed", form, firstLine(err.Error()))
	}
	var want cost
	switch {
	case form == "reset --hard", form == "reset --merge", form == "checkout -f":
		want = cost{Unstaged: got.Unstaged, Staged: got.Staged}
	case form == "checkout -- <paths>", form == "restore <paths>":
		want = cost{Unstaged: got.Unstaged}
	case strings.HasPrefix(form, "clean "):
		want = cost{Untracked: got.Untracked, Ignored: got.Ignored}
		if only {
			want.Untracked = 0 // -X removes only what is ignored
		}
	default: // worktree remove --force
		want = got
	}
	if want.zero() {
		return "", ""
	}
	return fmt.Sprintf("%s discards %s%s", form, want, where), ""
}

// statusCost counts the files git status lists in dir, for paths or for the
// whole tree: the measurement of discard-uncommitted.
func statusCost(r run.Runner, dir string, paths []string, ignored bool) (cost, error) {
	args := []string{"--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all"}
	if ignored {
		args = append(args, "--ignored=matching")
	}
	args = append(args, pathArgs(paths)...)
	out, err := objects.Git(r, dir, nil, args...)
	if err != nil {
		return cost{}, err
	}
	return parseStatus(out), nil
}

// DirtyOption narrows what Dirty counts.
type DirtyOption func(*cost)

// IgnoreUntracked leaves untracked files out of the count: for a checkout
// that only receives a merge, where git itself refuses to overwrite one.
func IgnoreUntracked() DirtyOption {
	return func(c *cost) { c.Untracked = 0 }
}

// Dirty describes the work in the tree at dir that no commit holds ("2
// unstaged files, 1 untracked file"), measured the way discard-uncommitted
// measures it, or "" for a clean tree. Files git ignores, including the
// repository's own exclude list, are never counted.
func Dirty(r run.Runner, dir string, opts ...DirtyOption) (string, error) {
	c, err := statusCost(r, dir, nil, false)
	if err != nil {
		return "", err
	}
	for _, o := range opts {
		o(&c)
	}
	return c.String(), nil
}

// measureBranch counts the commits `branch -D` would make unreachable from
// every other ref: HEAD, the other local branches and every remote-tracking
// ref. A branch merged locally or pushed anywhere reads zero: -D removes the
// local ref only, and the commits stay reachable.
// A branch that does not exist is git's own error to report.
func measureBranch(a *analysis, c gitCall, form string) string {
	names := strings.Fields(strings.TrimPrefix(form, "branch -D "))
	args := make([]string, 0, 2*len(names)+4)
	args = append(args, "rev-list", "--count")
	for _, n := range names {
		args = append(args, "refs/heads/"+n)
	}
	args = append(args, "--not")
	for _, n := range names {
		args = append(args, "--exclude=refs/heads/"+n)
	}
	args = append(args, "--all")
	out, err := objects.Git(a.r, c.Dir, nil, args...)
	if err != nil {
		return ""
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n); err != nil || n == 0 {
		return ""
	}
	return fmt.Sprintf("%s discards %s", form, cost{Commits: n})
}

// parseStatus counts the files `git status --porcelain=v1 -z` lists: a staged
// change in X, an unstaged one in Y, an untracked file as "??". A rename or
// copy is followed by one more record, the path it came from.
func parseStatus(out []byte) cost {
	var c cost
	recs := strings.Split(string(out), "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 || rec[2] != ' ' {
			continue
		}
		x, y := rec[0], rec[1]
		switch {
		case x == '?' && y == '?':
			c.Untracked++
		case x == '!' && y == '!':
			c.Ignored++
		default:
			if x != ' ' {
				c.Staged++
			}
			if y != ' ' {
				c.Unstaged++
			}
		}
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			i++
		}
	}
	return c
}

func pathArgs(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	return append([]string{"--"}, paths...)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// discardIntent classifies rest (the verb and its arguments, git's global
// options already stripped) as one of the discarding forms, or reports false
// for everything else, including a bare `git reset` and a branch-moving
// `git checkout <branch>`.
func discardIntent(rest []string) (form string, paths []string, ok bool) {
	if len(rest) == 0 {
		return "", nil, false
	}
	switch rest[0] {
	case "reset":
		return resetIntent(rest[1:])
	case "checkout":
		return checkoutIntent(rest[1:])
	case "restore":
		return restoreIntent(rest[1:])
	case "clean":
		return cleanIntent(rest[1:])
	case "stash":
		return stashIntent(rest[1:])
	case "branch":
		return branchIntent(rest[1:])
	case "worktree":
		return worktreeIntent(rest[1:])
	}
	return "", nil, false
}

func resetIntent(args []string) (string, []string, bool) {
	if slices.Contains(args, "--hard") {
		return "reset --hard", nil, true
	}
	if slices.Contains(args, "--merge") {
		return "reset --merge", nil, true
	}
	return "", nil, false
}

// checkoutIntent: `--` followed by pathspecs, or a bare `.`, discards the named
// paths; -f/--force alone discards the whole tree by moving the branch over it.
func checkoutIntent(args []string) (string, []string, bool) {
	for i, a := range args {
		if a == "--" {
			return "checkout -- <paths>", append([]string{}, args[i+1:]...), true
		}
	}
	if slices.Contains(args, ".") {
		return "checkout -- <paths>", []string{"."}, true
	}
	if slices.Contains(args, "-f") || slices.Contains(args, "--force") {
		return "checkout -f", nil, true
	}
	return "", nil, false
}

// restoreIntent: `--staged` alone touches only the index, so it is no discard;
// `--staged --worktree` (or the bundled `-SW`) touches the working tree too.
func restoreIntent(args []string) (string, []string, bool) {
	opts, ddPaths := splitDashDash(args)
	staged, worktree := false, false
	var operands []string
	for i := 0; i < len(opts); i++ {
		a := opts[i]
		switch {
		case a == "-s" || a == "--source":
			i++ // the revision to restore from, not a path
		case a == "--staged":
			staged = true
		case a == "--worktree":
			worktree = true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1:
			for _, ch := range a[1:] {
				switch ch {
				case 'S':
					staged = true
				case 'W':
					worktree = true
				}
			}
		case !strings.HasPrefix(a, "-"):
			operands = append(operands, a)
		}
	}
	if staged && !worktree {
		return "", nil, false
	}
	return "restore <paths>", append(operands, ddPaths...), true
}

// cleanIntent reads git clean's combinable short flags character by
// character: f (force) must be present and n (dry run) must not.
func cleanIntent(args []string) (string, []string, bool) {
	opts, ddPaths := splitDashDash(args)
	var flags, paths []string
	hasForce, hasDryRun := false, false
	for _, a := range opts {
		switch {
		case a == "--force":
			hasForce = true
			flags = append(flags, a)
		case a == "--dry-run":
			hasDryRun = true
			flags = append(flags, a)
		case strings.HasPrefix(a, "--"):
			flags = append(flags, a)
		case strings.HasPrefix(a, "-") && len(a) > 1:
			flags = append(flags, a)
			for _, ch := range a[1:] {
				switch ch {
				case 'f':
					hasForce = true
				case 'n':
					hasDryRun = true
				}
			}
		default:
			paths = append(paths, a)
		}
	}
	paths = append(paths, ddPaths...)
	if !hasForce || hasDryRun {
		return "", nil, false
	}
	return "clean " + strings.Join(flags, " "), paths, true
}

// stashIntent: only drop and clear discard; push is reversible with pop.
func stashIntent(args []string) (string, []string, bool) {
	if len(args) == 0 {
		return "", nil, false
	}
	switch args[0] {
	case "drop", "clear":
		return "stash " + args[0], nil, true
	}
	return "", nil, false
}

// branchIntent: any spelling of a forced delete (-D, --delete --force, -d -f,
// -fD) is `branch -D <names>`; a plain -d git refuses itself when unmerged.
func branchIntent(args []string) (string, []string, bool) {
	deleting, forced := false, false
	var names []string
	for _, a := range args {
		switch {
		case a == "-D":
			deleting, forced = true, true
		case a == "-d" || a == "--delete":
			deleting = true
		case a == "-f" || a == "--force":
			forced = true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1:
			if d, f, ok := branchDeleteBundle(a[1:]); ok {
				deleting = deleting || d
				forced = forced || f
			}
		case !strings.HasPrefix(a, "-"):
			names = append(names, a)
		}
	}
	if !deleting || !forced || len(names) == 0 {
		return "", nil, false
	}
	return "branch -D " + strings.Join(names, " "), nil, true
}

func branchDeleteBundle(chars string) (deleting, forced, ok bool) {
	for _, ch := range chars {
		switch ch {
		case 'd':
			deleting = true
		case 'D':
			deleting, forced = true, true
		case 'f':
			forced = true
		default:
			return false, false, false
		}
	}
	return deleting, forced, true
}

// worktreeIntent: only `remove --force` is a discard (an unforced remove
// refuses a dirty worktree itself); the path is paths[0].
func worktreeIntent(args []string) (string, []string, bool) {
	if len(args) == 0 || args[0] != "remove" {
		return "", nil, false
	}
	forced := false
	var path string
	for _, a := range args[1:] {
		switch a {
		case "-f", "--force":
			forced = true
		default:
			if !strings.HasPrefix(a, "-") {
				path = a
			}
		}
	}
	if !forced || path == "" {
		return "", nil, false
	}
	return "worktree remove --force", []string{path}, true
}

func splitDashDash(rest []string) (opts, paths []string) {
	for i, a := range rest {
		if a == "--" {
			return rest[:i], append([]string{}, rest[i+1:]...)
		}
	}
	return rest, nil
}

// cleanReach reads the short flags of a clean form: whether it reaches ignored
// files (`x` or `X`), and whether it reaches only those (`X`, which leaves the
// untracked files alone). A long option such as --exclude is no flag cluster.
// Source: aphrollo-tools 12b3724e internal/cli/git_shim_discard.go (cleanCost,
// untrackedCost). Incident: `git clean -fdx` on a clean tree deleted .env,
// which the measure never listed.
func cleanReach(form string) (ignored, only bool) {
	var hasX, hasBigX bool
	for _, f := range strings.Fields(form)[1:] {
		if !strings.HasPrefix(f, "-") || strings.HasPrefix(f, "--") {
			continue
		}
		hasX = hasX || strings.Contains(f, "x")
		hasBigX = hasBigX || strings.Contains(f, "X")
	}
	return hasX || hasBigX, hasBigX && !hasX
}
