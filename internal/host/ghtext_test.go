package host

import (
	"errors"
	"testing"
)

func TestGhSays_PinsEachFragmentOfGhOutput(t *testing.T) {
	for _, c := range []struct {
		name, fragment, stderr string
	}{
		{"no checks", ghNoChecks, "exit status 1: no checks reported on the 'feat' branch"},
		{"head modified", ghHeadModified, "exit status 1: GraphQL: Head branch was modified. Review and try the merge again. (mergePullRequest)"},
	} {
		if !ghSays(errors.New(c.stderr), c.fragment) {
			t.Errorf("%s: %q not found in %q", c.name, c.fragment, c.stderr)
		}
	}
	if got, want := ghNoChecks, "no checks reported"; got != want {
		t.Errorf("ghNoChecks = %q, want the exact gh text %q", got, want)
	}
	if got, want := ghHeadModified, "head branch was modified"; got != want {
		t.Errorf("ghHeadModified = %q, want the exact gh text %q", got, want)
	}
	if ghSays(nil, ghNoChecks) || ghSays(errors.New("exit status 1: HTTP 502"), ghNoChecks) {
		t.Error("ghSays matched an error that does not say it")
	}
}
