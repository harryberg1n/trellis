package walls

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/run"
)

// primaryWrite is the primary-write wall (#1043): an Edit or Write, or a shell
// write (a redirect, `sed -i`, `tee`, `cp`, `mv`), into the primary checkout
// while it is on the default branch. Agents work in trees; the primary is
// where the default branch is merged and released, and a write there lands on
// the default branch without review. The path is judged where it resolves, so
// a tree's session writing to the primary by absolute path is refused too.
func primaryWrite(a *analysis) string {
	if a.file != "" {
		return primaryCause(a.r, a.tool+" of", a.file)
	}
	for _, target := range a.writes() {
		if cause := primaryCause(a.r, "a shell write to", target); cause != "" {
			return cause
		}
	}
	return ""
}

// isIgnored reports whether git ignores rel in the checkout at root: `git
// check-ignore -q` exits 0 for an ignored path. A runner that cannot say
// leaves the path guarded.
func isIgnored(r run.Runner, root, rel string) bool {
	if r == nil {
		return false
	}
	res, err := r.Light(run.Spec{Name: "git", Args: []string{"check-ignore", "-q", "--", filepath.ToSlash(rel)}, Dir: root, Timeout: 10 * time.Second})
	return err == nil && res.ExitCode == 0
}

func primaryCause(r run.Runner, what, target string) string {
	f, ok := repoAt(filepath.Dir(target))
	if !ok || !onDefault(f) {
		return ""
	}
	rel, err := filepath.Rel(f.Root, target)
	if err != nil {
		rel = target
	}
	// Git's own directory is not the working tree, and an ignored path is not
	// on the default branch: neither lands anything without review.
	if first, _, _ := strings.Cut(filepath.ToSlash(rel), "/"); first == ".git" || isIgnored(r, f.Root, rel) {
		return ""
	}
	return fmt.Sprintf("%s %s is in the primary checkout, which is on %s", what, filepath.ToSlash(rel), f.Branch)
}
