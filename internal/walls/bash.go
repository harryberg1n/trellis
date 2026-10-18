package walls

import (
	"fmt"
	"slices"
	"strings"

	"github.com/harryberg1n/trellis/internal/objects"
)

// mainCommit is the main-commit wall (#1043): `git commit`, `git push` or
// `git merge` run in the primary checkout while it is on the default branch.
// The default branch moves by merging a tree, not by a commit made where it is
// checked out. A command that only reads or undoes (--dry-run, merge --abort)
// is let through, and so is a merge that names nothing but the branch's own
// upstream: moving main to origin/main lands nothing unjudged.
func mainCommit(a *analysis) string {
	for _, c := range a.gitCalls() {
		if !slices.Contains([]string{"commit", "push", "merge"}, c.Verb) || harmless(c) {
			continue
		}
		f, ok := repoAt(c.Dir)
		if !ok || !onDefault(f) {
			continue
		}
		if c.Verb == "merge" && onlyNamesUpstream(f, c.Args) {
			continue
		}
		return fmt.Sprintf("git %s in the primary checkout, which is on %s", c.Verb, f.Branch)
	}
	return ""
}

// harmless reports whether the call changes nothing: a dry run, or the end of
// a merge in progress.
func harmless(c gitCall) bool {
	if slices.Contains(c.Args, "--dry-run") {
		return true
	}
	return c.Verb == "merge" && (slices.Contains(c.Args, "--abort") || slices.Contains(c.Args, "--quit"))
}

// onlyNamesUpstream reports whether a merge names nothing but the current
// branch's own upstream, read from branch.<name>.remote and branch.<name>.merge
// in the config without a process. Source: aphrollo-tools 12b3724e,
// internal/cli/git_shim_upstream.go (onlyNamesUpstream); the incident there is
// the ordinary loop of a pull-request-only repository, where the primary stays
// behind origin/main unless it may fast-forward to it. Found again on the
// fanpyp box, 2026-10-18, as the first deny on honest work of the live run.
//
// A merge with no operand takes the upstream by definition. With operands, all
// of them must resolve to it: `origin/main`, `@{upstream}`, `@{u}`, or the
// two-token `origin main`. Anything else, a tree branch, a second ref, a sha,
// is refused as before. `--no-ff` stays refused: it makes a commit on the
// default branch in the primary, which is what this wall is for.
func onlyNamesUpstream(r objects.Repo, args []string) bool {
	if slices.Contains(args, "--no-ff") {
		return false
	}
	remote, found, err := objects.ConfigValue(r.GitDir, `branch "`+r.Branch+`"`, "remote")
	if err != nil || !found || remote == "" {
		return false // no upstream to fast-forward to; the wall stands
	}
	merge, found, err := objects.ConfigValue(r.GitDir, `branch "`+r.Branch+`"`, "merge")
	if err != nil || !found || merge == "" {
		return false
	}
	branch := strings.TrimPrefix(merge, "refs/heads/")
	upstream := remote + "/" + branch

	var operands []string
	for _, a := range args {
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		operands = append(operands, a)
	}
	switch len(operands) {
	case 0:
		return true
	case 1:
		return operands[0] == upstream || operands[0] == "@{upstream}" || operands[0] == "@{u}"
	case 2:
		return operands[0] == remote && operands[1] == branch
	}
	return false
}
