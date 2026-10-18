package walls

import (
	"fmt"
	"slices"
)

// mainCommit is the main-commit wall (#1043): `git commit`, `git push` or
// `git merge` run in the primary checkout while it is on the default branch.
// The default branch moves by merging a lane, not by a commit made where it is
// checked out. A command that only reads or undoes (--dry-run, merge --abort)
// is let through.
func mainCommit(a *analysis) string {
	for _, c := range a.gitCalls() {
		if !slices.Contains([]string{"commit", "push", "merge"}, c.Verb) || harmless(c) {
			continue
		}
		f, ok := repoAt(c.Dir)
		if !ok || !f.onDefault() {
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
