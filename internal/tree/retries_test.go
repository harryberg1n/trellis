package tree

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
)

// Issue #52 (threadspyp, GitHub's partial outage of 2026-11-04): one lane
// needed six or more `tree merge` runs, each paying for the full --check
// again: a push answered 500, a merge answered a GraphQL error, and GitHub
// still showed the old head right after trellis's own push.

// flakyGit fails the first len(errs) git calls that start with prefix.
type flakyGit struct {
	Inner  run.Runner
	Prefix []string
	mu     *sync.Mutex
	errs   *[]string
}

func newFlakyGit(inner run.Runner, prefix []string, errs ...string) flakyGit {
	return flakyGit{Inner: inner, Prefix: prefix, mu: &sync.Mutex{}, errs: &errs}
}

func (f flakyGit) Light(spec run.Spec) (run.Result, error) {
	if spec.Name == "git" && len(spec.Args) >= len(f.Prefix) && slices.Equal(spec.Args[:len(f.Prefix)], f.Prefix) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(*f.errs) > 0 {
			e := (*f.errs)[0]
			*f.errs = (*f.errs)[1:]
			return run.Result{ExitCode: 1}, errors.New(e)
		}
	}
	return f.Inner.Light(spec)
}

func TestMerge_APushThatMeetsAServerErrorIsTriedAgain(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	env := f.env(path)
	env.Runner = newFlakyGit(f.gh, []string{"push", "--force-with-lease"}, "git: exit status 1: remote: Internal Server Error")

	if err := Merge(env, ""); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(f.out.String(), "push of feat: GitHub could not be reached (1 of 5 tries): remote: Internal Server Error; trying again in 5s\n") {
		t.Errorf("output = %q, want the retry line of the push", f.out.String())
	}
	if !slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("kinds = %v, want merged", f.kinds())
	}
}

func TestMerge_AMergeThatMeetsAGraphQLErrorIsTriedAgain(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.flaky = map[string][]string{"pr merge": {"GraphQL: Something went wrong while executing your query. Please include `C0DE:1` when reporting this issue."}}

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if n := strings.Count(strings.Join(f.ghCalls(), ","), "pr merge"); n != 2 {
		t.Errorf("pr merge calls = %d, want 2", n)
	}
	if !slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("kinds = %v, want merged", f.kinds())
	}
}

// A 502 can come back from a merge GitHub made: the PR is merged and a second
// merge would answer "Merge already in progress" or "not mergeable".
func TestMerge_AServerErrorFromAMergeThatLandedIsAMerge(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeErr = "HTTP 502: Bad Gateway (https://api.github.com/graphql)"
	f.landed = true

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if n := strings.Count(strings.Join(f.ghCalls(), ","), "pr merge"); n != 1 {
		t.Errorf("pr merge calls = %d, want 1: the PR was merged", n)
	}
	if !slices.Contains(f.kinds(), "tree.merged") || exists(path) {
		t.Errorf("kinds %v, tree exists %v; want merged and removed", f.kinds(), exists(path))
	}
}

func TestMerge_MergeAlreadyInProgressIsWaitedOut(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.flaky = map[string][]string{"pr merge": {"GraphQL: Merge already in progress (mergePullRequest)"}}

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("kinds = %v, want merged", f.kinds())
	}
}

func TestMerge_ARefusedMergeIsNotTriedAgain(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	f.mergeErr = "GraphQL: Pull request is not mergeable: the base branch policy prohibits the merge"

	err := Merge(f.env(path), "")

	if err == nil || !strings.HasPrefix(err.Error(), "tree merge: merge failed: ") || !strings.Contains(err.Error(), "GraphQL: Pull request is not mergeable") {
		t.Errorf("err = %v, want the merge failure", err)
	}
	if n := strings.Count(strings.Join(f.ghCalls(), ","), "pr merge"); n != 1 {
		t.Errorf("pr merge calls = %d, want 1", n)
	}
}

// Right after trellis's own push, `git ls-remote` showed the new head and the
// PR API still answered the old one; the merge failed with "head moved".
func TestMerge_WaitsForGitHubToShowThePushedHead(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	stale := testiso.Git(t, path, "rev-parse", "HEAD~1")
	pushed := testiso.Git(t, path, "rev-parse", "HEAD")
	f.headSeq = []string{stale, stale, stale}

	if err := Merge(f.env(path), ""); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(f.out.String(), "GitHub still shows the PR head at "+stale[:12]+", not the pushed "+pushed[:12]+": waiting\n") {
		t.Errorf("output = %q, want the waiting line", f.out.String())
	}
	if !slices.Contains(f.kinds(), "tree.merged") {
		t.Errorf("kinds = %v, want merged", f.kinds())
	}
}

func TestMerge_AHeadGitHubNeverShowsStopsAfterTwoMinutes(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	f.head = "0123456789abcdef0123456789abcdef01234567"

	err := Merge(f.env(path), "")

	local := testiso.Git(t, path, "rev-parse", "HEAD")[:12]
	want := "tree merge: head moved: local " + local + " vs PR 0123456789ab"
	if fixable(err) == nil || err.Error() != want {
		t.Errorf("err = %v, want fixable %q", err, want)
	}
	var waited time.Duration
	for _, d := range f.clk.waits {
		waited += d
	}
	if waited != 2*time.Minute {
		t.Errorf("waited %v on the clock, want 2m0s", waited)
	}
	if slices.Contains(f.ghCalls(), "pr merge") || slices.Contains(f.ghCalls(), "pr checks") {
		t.Errorf("gh calls = %v, want no checks and no merge", f.ghCalls())
	}
}

// A merge that failed after a green check ran the whole check again on the
// next try, though the head was the same.
func TestMerge_AGreenCheckOfTheSameHeadIsNotRunAgain(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	f.mergeErr = "GraphQL: Pull request is not mergeable"
	if err := Merge(f.env(path), "exit 0"); err == nil {
		t.Fatal("first merge succeeded, want the refused merge")
	}
	f.mergeErr = ""
	f.out.Reset()
	head := testiso.Git(t, path, "rev-parse", "HEAD")[:12]

	if err := Merge(f.env(path), "exit 0"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(f.out.String(), "check passed on "+head+" already: not run again\n") || strings.Contains(f.out.String(), "check passed\n") {
		t.Errorf("output = %q, want the check skipped", f.out.String())
	}
}

func TestMerge_ACheckRunsAgainOnANewHeadOrAnotherCommand(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	f.mergeErr = "GraphQL: Pull request is not mergeable"
	if err := Merge(f.env(path), "exit 0"); err == nil {
		t.Fatal("first merge succeeded, want the refused merge")
	}
	f.out.Reset()

	if err := Merge(f.env(path), "exit 0 "); err == nil { // another command
		t.Fatal("second merge succeeded, want the refused merge")
	}
	if !strings.Contains(f.out.String(), "check passed\n") {
		t.Errorf("another command: output = %q, want the check run", f.out.String())
	}

	commitFile(t, path, "more.txt", "more\n")
	f.mergeErr = ""
	f.out.Reset()
	if err := Merge(f.env(path), "exit 0"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "check passed\n") {
		t.Errorf("new head: output = %q, want the check run", f.out.String())
	}
}

func TestMerge_ARedCheckIsRunAgain(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	if err := Merge(f.env(path), "exit 1"); err == nil {
		t.Fatal("merge with a red check succeeded")
	}
	f.out.Reset()

	if err := Merge(f.env(path), "exit 1"); err == nil || strings.Contains(f.out.String(), "already") {
		t.Errorf("err = %v, output = %q; want the red check run again", err, f.out.String())
	}
}

// Issue #52 item 1: the delete of the merged branch met a 500 and the branch
// stayed on origin until it was deleted by hand.
func TestMerge_ADeleteOfTheOriginBranchThatMeetsAServerErrorIsTriedAgain(t *testing.T) {
	f := newFixture(t)
	path := f.openTree(t)
	commitFile(t, path, "mine.txt", "mine\n")
	env := f.env(path)
	f.mergeSHA = "0123456789abcdef0123456789abcdef01234567"
	env.Runner = newFlakyGit(f.gh, []string{"push", "origin", "--delete"}, "git: exit status 1: remote: Internal Server Error")

	if err := Merge(env, ""); err != nil {
		t.Fatal(err)
	}

	if got := testiso.Git(t, f.origin, "branch", "--list", "feat"); got != "" {
		t.Errorf("origin still has feat (%q)", got)
	}
	if f.errb.Len() != 0 {
		t.Errorf("warnings = %q, want none", f.errb.String())
	}
}
