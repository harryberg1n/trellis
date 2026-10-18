package host

import "strings"

// The text of gh that trellis acts on. gh has no machine-readable error
// codes, so these are the exact fragments of its stderr, read from gh 2.89.0
// (2026-03-26, the gh the strings were checked against); an older or newer gh
// that words them differently turns a missing-checks answer into a plain
// error, which stops the merge rather than letting it go on. Every match goes
// through ghSays, which ignores case, and ghtext_test.go pins each fragment.
const (
	// ghNoChecks is `gh pr checks` on a pull request GitHub has no check runs
	// for: "no checks reported on the '<branch>' branch".
	ghNoChecks = "no checks reported"
	// ghHeadModified is `gh pr merge --match-head-commit <sha>` on a pull
	// request whose head is another commit: GitHub's "Head branch was modified.
	// Review and try the merge again." (aphrollo-tools
	// internal/integrate/host/github/queue.go at 12b3724e, HeadMovedText;
	// incident #1174, a merge that landed a head nobody had judged).
	ghHeadModified = "head branch was modified"
)

// ghSays reports whether err carries the gh fragment text, in any case.
func ghSays(err error, text string) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), strings.ToLower(text))
}
