package walls

import (
	"fmt"
	"path/filepath"
)

// primaryWrite is the primary-write wall (#1043): an Edit or Write, or a shell
// write (a redirect, `sed -i`, `tee`, `cp`, `mv`), into the primary checkout
// while it is on the default branch. Agents work in trees; the primary is
// where the default branch is merged and released, and a write there lands on
// the default branch without review. The path is judged where it resolves, so
// a tree's session writing to the primary by absolute path is refused too.
func primaryWrite(a *analysis) string {
	if a.file != "" {
		return primaryCause(a.tool+" of", a.file)
	}
	for _, target := range a.writes() {
		if cause := primaryCause("a shell write to", target); cause != "" {
			return cause
		}
	}
	return ""
}

func primaryCause(what, target string) string {
	f, ok := repoAt(filepath.Dir(target))
	if !ok || !onDefault(f) {
		return ""
	}
	rel, err := filepath.Rel(f.Root, target)
	if err != nil {
		rel = target
	}
	return fmt.Sprintf("%s %s is in the primary checkout, which is on %s", what, filepath.ToSlash(rel), f.Branch)
}
