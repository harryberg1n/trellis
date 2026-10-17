package store

import "testing"

func TestRepoID_PathIsCleanedAndCaseFoldedOnWindows(t *testing.T) {
	// sha256 of the folded path c:\repo, first 16 hex digits.
	const want = "c4866a467d261ea3"
	for _, p := range []string{`C:\Repo`, `c:\repo\`, `C:\REPO`, `c:/repo`} {
		if got := RepoID("", p); got != want {
			t.Errorf("RepoID(%q) = %s, want %s", p, got, want)
		}
	}
}
