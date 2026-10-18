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
