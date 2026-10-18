package host

import "strings"

// The text of gh that trellis acts on. gh has no machine-readable error
// codes, so these are the exact fragments of its stderr, read from gh 2.89.0
// (2026-03-26, the gh the strings were checked against); an older or newer gh
// that words them differently turns a missing-checks answer into a plain
// error, which stops the merge rather than letting it go on. Every match goes
// through ghSays, and ghtext_test.go pins each fragment.
const (
	// ghNoChecks is `gh pr checks` on a pull request GitHub has no check runs
	// for: "no checks reported on the '<branch>' branch".
	ghNoChecks = "no checks reported"
)

// ghSays reports whether err carries the gh fragment text.
func ghSays(err error, text string) bool {
	return err != nil && strings.Contains(err.Error(), text)
}
