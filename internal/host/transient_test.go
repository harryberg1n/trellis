package host

import (
	"errors"
	"testing"
	"time"
)

func TestTransient_ReadsTheMarkersOfAFailureToReachGitHub(t *testing.T) {
	for _, c := range []struct {
		stderr string
		want   bool
	}{
		{"exit status 1: HTTP 502: Bad Gateway (https://api.github.com/graphql)", true},
		{"exit status 1: HTTP 429: rate limited", true},
		{"exit status 1: HTTP 500", true},
		{"exit status 1: HTTP 503", true},
		{"exit status 1: HTTP 504", true},
		{"exit status 1: unexpected end of JSON input", true},
		{"host: gh pr checks: timed out after 60s", true},
		// GitHub's partial outage of 2026-11-04 (#52): a git push, a GraphQL merge.
		{"git: exit status 1: remote: Internal Server Error", true},
		{"exit status 1: GraphQL: Something went wrong while executing your query. Please include `C0DE:1` when reporting this issue.", true},
		{"fatal: unable to access 'https://github.com/o/r.git/': The requested URL returned error: 502", true},
		{"fatal: unable to access 'https://github.com/o/r.git/': The requested URL returned error: 503", true},
		{"remote: Service Unavailable", true},
		{"exit status 1: GraphQL: Merge already in progress (mergePullRequest)", false},
		{"fatal: unable to access 'https://github.com/o/r.git/': The requested URL returned error: 403", false},
		{"exit status 1: HTTP 404: Not Found", false},
		{"exit status 1: no checks reported on the 'feat' branch", false},
		{"exit status 1: HTTP 5000 widgets", false},
	} {
		if got := Transient(errors.New(c.stderr)); got != c.want {
			t.Errorf("Transient(%q) = %v, want %v", c.stderr, got, c.want)
		}
	}
	if Transient(nil) {
		t.Error("Transient(nil) = true")
	}
}

func TestRetryAfter_ReadsTheSecondsAndCapsThemAtTwoMinutes(t *testing.T) {
	for _, c := range []struct {
		stderr string
		want   time.Duration
	}{
		{"HTTP 429: Retry-After: 30", 30 * time.Second},
		{"HTTP 429 retry after 7", 7 * time.Second},
		{"HTTP 429: Retry-After: 9000", 2 * time.Minute},
		{"HTTP 429: slow down", 0},
	} {
		if got := RetryAfter(errors.New(c.stderr)); got != c.want {
			t.Errorf("RetryAfter(%q) = %v, want %v", c.stderr, got, c.want)
		}
	}
}

func TestTransient_OfflineDNSIsAFailureToReachGitHub(t *testing.T) {
	err := errors.New("exit status 1: error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com")
	if !Transient(err) {
		t.Error("Transient = false for gh's offline message")
	}
}
