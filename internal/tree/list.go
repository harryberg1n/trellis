package tree

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/host"
	"github.com/harryberg1n/trellis/internal/store"
)

// Issue #50 item 2 (fanpyp, 2026-10-25): 41 worktrees (17 GB), made by
// trellis and before it, and 13 orphan directories whose .git pointed at
// pruned metadata. `tree rm` removed one trellis tree at a time and nothing
// said which were safe to drop, so the cleanup was scripted. `tree list`
// says, for every worktree git knows, what `tree prune` would do with it;
// `tree prune` does it. The rule is the reference's
// (aphrollo-tools 12b3724e, internal/workspace/prune.go, decide): remove only
// a clean tree whose pull request merged with this very tip, never the tree
// the caller stands in, and keep on any doubt (a gh that cannot answer).

// Verdicts of a tree, as tree list prints them.
const (
	verdictRemove  = "remove"
	verdictMissing = "prune(missing)"
	verdictOrphan  = "orphan"
)

// treeRow is one worktree of the repository and what prune does with it.
type treeRow struct {
	name    string // the branch, or "(detached)"
	path    string
	missing bool // git lists it, its directory is gone
	ahead   int  // commits not in the default branch; -1 unknown
	files   int  // files git status names; -1 unknown
	pr      string
	age     string
	verdict string // verdictRemove, verdictMissing, or keep(<reason>)
	size    int64  // bytes on disk, with sizes
}

// keepReason is the reason in a keep(...) verdict, "" for any other verdict.
func (r treeRow) keepReason() string {
	if s, ok := strings.CutPrefix(r.verdict, "keep("); ok {
		return strings.TrimSuffix(s, ")")
	}
	return ""
}

// List prints every worktree of the repository but the primary checkout,
// with its commits ahead of the default branch, its uncommitted files, its
// pull request, the age of its last commit and what `tree prune` would do,
// then the sibling directories that look like trees and are none. sizes adds
// each tree's size on disk.
func List(env Env, sizes bool) error {
	return env.interrupted("list", listTrees(env, sizes))
}

func listTrees(env Env, sizes bool) error {
	w, err := env.locate("list")
	if err != nil {
		return err
	}
	rows, orphans, err := survey(env, w, "list")
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(env.Out, 0, 0, 2, ' ', 0)
	header := "TREE\tAHEAD\tFILES\tPR\tAGE\tPRUNE\tPATH"
	if sizes {
		header = "TREE\tAHEAD\tFILES\tPR\tAGE\tSIZE\tPRUNE\tPATH"
	}
	_, _ = fmt.Fprintln(tw, header)
	for _, r := range rows {
		cells := []string{r.name, count(r.ahead), count(r.files), r.pr, r.age}
		if sizes {
			r.size = dirSize(r.path)
			cells = append(cells, humanSize(r.size))
		}
		cells = append(cells, r.verdict, r.path)
		_, _ = fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	for _, o := range orphans {
		cells := []string{"-", "-", "-", "-", "-"}
		if sizes {
			cells = append(cells, humanSize(dirSize(o)))
		}
		cells = append(cells, verdictOrphan, o)
		_, _ = fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

// Prune removes every tree whose pull request merged with its tip and that
// holds no uncommitted work, and forgets the trees whose directory is gone.
// backup also removes every other tree after keeping all it held, as
// `tree drop` does, under refs/trellis/dropped/ and, with a remote, on
// origin as backup/<name>. The tree the caller stands in is kept. Orphan
// directories are named, never deleted: git knows nothing of what they hold.
// dry says what would happen and changes nothing.
func Prune(env Env, dry, backup bool) error {
	return env.interrupted("prune", pruneTrees(env, dry, backup))
}

func pruneTrees(env Env, dry, backup bool) error {
	w, err := env.locate("prune")
	if err != nil {
		return err
	}
	rows, orphans, err := survey(env, w, "prune")
	if err != nil {
		return err
	}
	var missing []string
	for _, r := range rows {
		reason := r.keepReason()
		switch {
		case r.verdict == verdictMissing:
			missing = append(missing, r.name)
		case reason == "you-are-here":
			env.say("kept %s: you are in it", r.name)
		case r.verdict == verdictRemove && dry:
			env.say("would remove %s %s", r.name, r.path)
		case r.verdict == verdictRemove:
			if err := removeTree(env, w, r.path, r.name, false); err != nil {
				return fmt.Errorf("tree prune: %w", err)
			}
		case backup && reason != "detached" && dry:
			env.say("would back up and remove %s %s", r.name, r.path)
		case backup && reason != "detached":
			if err := backUpAndRemove(env, w, r); err != nil {
				return err
			}
		default:
			env.say("kept %s: %s", r.name, reason)
		}
	}
	if len(missing) > 0 {
		if dry {
			for _, name := range missing {
				env.say("would prune %s", name)
			}
		} else {
			if _, err := env.runGit(w.Primary, "worktree", "prune"); err != nil {
				return fmt.Errorf("tree prune: %w", err)
			}
			for _, name := range missing {
				env.say("pruned %s: its directory was gone", name)
			}
		}
	}
	for _, o := range orphans {
		env.say("orphan %s: not a worktree of this repository; remove it by hand if it holds nothing", o)
	}
	return nil
}

// backUpAndRemove keeps the tree's work under refs/trellis/dropped/ and on
// origin as backup/<name>, records the drop, and removes the tree.
func backUpAndRemove(env Env, w where, r treeRow) error {
	tip, err := keepWork(env, r.path, r.name)
	if err != nil {
		return fmt.Errorf("tree prune: %s: %w", r.name, err)
	}
	ref, err := keepRef(env, w.Primary, r.name, tip)
	if err != nil {
		return fmt.Errorf("tree prune: %s: %w", r.name, err)
	}
	where := ref
	if w.Repo.Remote != "" {
		if _, err := env.runGit(w.Primary, "push", remoteOf(w.Repo), "+"+tip+":refs/heads/backup/"+r.name); err != nil {
			env.warn("%s was not pushed to %s backup/%s: %s; it is kept at %s", r.name, remoteOf(w.Repo), r.name, firstLine(err), ref)
		} else {
			where += " and " + remoteOf(w.Repo) + " backup/" + r.name
		}
	}
	env.record(droppedEvent(r.name, "tree prune --backup", ref))
	env.say("backed up %s at %s", r.name, where)
	return removeTree(env, w, r.path, r.name, true)
}

// survey reads every worktree but the primary, sorted by name, and the
// orphan directories beside the primary.
func survey(env Env, w where, verb string) ([]treeRow, []string, error) {
	if w.Repo.Remote != "" {
		if _, err := env.runGit(w.Primary, "fetch", "--prune", remoteOf(w.Repo)); err != nil {
			return nil, nil, fmt.Errorf("tree %s: %w", verb, err)
		}
	}
	out, err := env.runGit(w.Primary, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, nil, fmt.Errorf("tree %s: %w", verb, err)
	}
	clk := env.Clock
	if clk == nil {
		clk = clock.Real()
	}
	base := baseOf(w.Repo)
	here := store.Canonical(env.Dir)
	known := []string{store.Canonical(w.Primary)}
	var rows []treeRow
	for _, block := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n\n") {
		r, ok := parseWorktree(block)
		if !ok {
			continue
		}
		r.path = store.Canonical(r.path)
		known = append(known, r.path)
		if r.path == store.Canonical(w.Primary) {
			continue
		}
		judge(env, w, base, clk.Now(), here, &r)
		rows = append(rows, r)
	}
	slices.SortFunc(rows, func(a, b treeRow) int { return strings.Compare(a.name, b.name) })
	return rows, orphansOf(w.Primary, known), nil
}

// parseWorktree reads one block of `git worktree list --porcelain`.
func parseWorktree(block string) (treeRow, bool) {
	r := treeRow{ahead: -1, files: -1}
	for _, line := range strings.Split(strings.TrimSpace(block), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			r.path = filepath.FromSlash(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch refs/heads/"):
			r.name = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "detached":
			r.name = "(detached)"
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			r.missing = true
		}
	}
	return r, r.path != ""
}

// judge fills in what a row needs from git and gh, and its verdict.
func judge(env Env, w where, base string, now time.Time, here string, r *treeRow) {
	if _, err := os.Stat(r.path); err != nil {
		r.missing = true
	}
	detached := r.name == "(detached)"
	ref := r.name
	if detached {
		ref = "HEAD"
	}
	dir := w.Primary
	if detached && !r.missing {
		dir = r.path
	}
	if !detached {
		if n, err := env.countCommits(dir, base+".."+ref); err == nil {
			r.ahead = n
		}
	}
	if !r.missing {
		if out, err := env.runGit(r.path, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all"); err == nil {
			r.files = statusCount(out)
		}
	}
	r.age = "-"
	if ct, err := env.runGit(dir, "log", "-1", "--format=%ct", ref); err == nil {
		if s, err := strconv.ParseInt(ct, 10, 64); err == nil {
			r.age = age(now.Sub(time.Unix(s, 0)))
		}
	}
	r.pr = "-"
	if w.Repo.Remote != "" && !detached {
		r.pr = prState(env, w, r.name)
	}
	_, inside := store.Within(r.path, here)
	switch {
	case r.missing:
		r.verdict = verdictMissing
	case inside:
		r.verdict = "keep(you-are-here)"
	case detached:
		r.verdict = "keep(detached)"
	case r.files != 0:
		r.verdict = "keep(uncommitted)" // also when unknown: keep on doubt
	case r.pr == "open":
		r.verdict = "keep(open-pr)"
	case landed(env, w, r.name, base):
		r.verdict = verdictRemove
	case r.ahead != 0:
		r.verdict = "keep(unmerged)"
	default:
		r.verdict = "keep(no-merge)"
	}
}

// prState is the state of the branch's pull request in lower case, "none",
// or "?" when gh cannot answer.
func prState(env Env, w where, name string) string {
	st, err := (host.GH{Runner: env.runner(), Dir: w.Primary}).PRView(name)
	switch {
	case errors.Is(err, host.ErrNoPR):
		return "none"
	case err != nil:
		return "?"
	}
	return strings.ToLower(st.State)
}

// statusCount is the number of files `git status --porcelain=v1 -z` names.
func statusCount(out string) int {
	n := 0
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 {
			continue
		}
		n++
		if rec[0] == 'R' || rec[0] == 'C' {
			i++ // the path it came from
		}
	}
	return n
}

// orphansOf are the directories beside primary named as trees are
// (<primary>--*) that git does not list as worktrees.
func orphansOf(primary string, known []string) []string {
	matches, _ := filepath.Glob(primary + "--*")
	var out []string
	for _, m := range matches {
		if info, err := os.Stat(m); err != nil || !info.IsDir() {
			continue
		}
		if c := store.Canonical(m); !slices.Contains(known, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out
}

func count(n int) string {
	if n < 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

// age is "3d" from two days on, else "5h".
func age(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d >= 48*time.Hour {
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	}
	return strconv.Itoa(int(d/time.Hour)) + "h"
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry is left out of the size
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func humanSize(b int64) string {
	switch {
	case b >= 1<<30:
		return strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64) + "G"
	case b >= 1<<20:
		return strconv.FormatFloat(float64(b)/(1<<20), 'f', 1, 64) + "M"
	}
	return strconv.FormatInt(b/1024, 10) + "K"
}
