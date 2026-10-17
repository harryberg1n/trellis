//go:build !windows

package store

import "testing"

func TestRepoID_PathIsCleanedAndCaseSensitive(t *testing.T) {
	// sha256 of /a/b, first 16 hex digits.
	const want = "662b7b62a798bb2d"
	for _, p := range []string{"/a/b", "/a/b/", "/a//b", "/a/./b"} {
		if got := RepoID("", p); got != want {
			t.Errorf("RepoID(%q) = %s, want %s", p, got, want)
		}
	}
	if RepoID("", "/a/B") == want {
		t.Error("/a/B must differ from /a/b")
	}
}
