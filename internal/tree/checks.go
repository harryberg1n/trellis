package tree

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/host"
	"github.com/harryberg1n/trellis/internal/objects"
)

// GitHub creates a pull request's check runs a few seconds after the push, so
// a read right after it can show none. The poll waits checksWindow for the
// first one, checksDelay between reads. A workflow run of the head that is
// still queued (behind another run of its concurrency group) has made no
// check yet: the poll goes on waiting for it, for at most queuedWindow.
const (
	checksWindow = 90 * time.Second
	checksDelay  = 5 * time.Second
	queuedWindow = 30 * time.Minute
)

// awaitChecks polls the checks of pull request pr until one exists, and
// reports whether one did. None within checksWindow is accepted only when the
// repository defines no workflows or GitHub calls the pull request CLEAN and no
// workflow run of head (the commit the checks are judged on) is queued or in
// progress; any other merge state is a Fixable, since checks may still be on
// their way.
func awaitChecks(env Env, gh host.GH, pr int, branch, head string) (bool, error) {
	noWorkflows := false
	for waited := time.Duration(0); ; waited += checksDelay {
		var checks []host.Check
		err := env.retry(fmt.Sprintf("checks of #%d", pr), func() (err error) {
			checks, err = gh.Checks(pr)
			return err
		})
		if err != nil {
			return false, fmt.Errorf("tree merge: checks could not be read: %s", firstLine(err))
		}
		if len(checks) > 0 {
			return true, nil
		}
		if waited == 0 {
			// a repository with no workflows never gets a check: nothing to wait for
			n, werr := gh.WorkflowCount()
			noWorkflows = werr == nil && n == 0
		}
		if noWorkflows {
			return false, nil
		}
		if waited >= checksWindow && !runOpenOn(gh, branch, head) {
			break
		}
		if waited >= queuedWindow {
			return false, &Fixable{Msg: fmt.Sprintf("tree merge: no checks appeared and a run of this head is still queued or in progress after %ds: run again",
				int(queuedWindow.Seconds()))}
		}
		env.wait(checksDelay)
	}
	st, err := gh.PRView(branch)
	if err != nil {
		return false, fmt.Errorf("tree merge: %w", err)
	}
	if !strings.EqualFold(st.MergeStateStatus, "CLEAN") {
		return false, &Fixable{Msg: fmt.Sprintf("tree merge: no checks appeared in %ds and the merge state is %s: run again",
			int(checksWindow.Seconds()), st.MergeStateStatus)}
	}
	return false, nil
}

// runOpenOn reports whether a pull_request workflow run of head is queued or in
// progress. A run list that cannot be read counts none: the poll then judges
// the checks there are, as it did before the runs were looked at. Source:
// aphrollo-tools internal/workspace/mergewait.go at 12b3724e
// (requiredRunsOpen); incident merge-path: a PR merged with its required check
// not made yet because the run was still queued.
func runOpenOn(gh host.GH, branch, head string) bool {
	runs, err := gh.PullRequestRuns(branch)
	if err != nil {
		return false
	}
	for _, r := range runs {
		if strings.EqualFold(r.HeadSHA, head) && r.Open() {
			return true
		}
	}
	return false
}

// watchAndReady waits for the checks to finish, then judges the pull request,
// and returns the head commit it judged. A merge state of UNSTABLE or BLOCKED
// while a check is still pending is not a verdict: GitHub counted a check the
// watch had not seen, so watch again, for at most checksWindow of waiting.
// watch is false when the pull request has no check to watch.
func watchAndReady(env Env, gh host.GH, repo objects.Repo, branch string, pr int, watch bool) (string, error) {
	for waited := time.Duration(0); ; waited += checksDelay {
		if watch {
			err := env.retry(fmt.Sprintf("checks of #%d", pr), func() (err error) {
				_, err = gh.WatchChecks(pr)
				return err
			})
			switch {
			case errors.Is(err, host.ErrNoChecks):
			case err != nil:
				return "", checksError(env, gh, pr, err)
			default:
				env.sayWait("checks passed on #%d", pr)
			}
		}
		judged, pending, err := readyToMerge(env, gh, repo, branch)
		if err != nil {
			return "", err
		}
		if !pending {
			return judged, nil
		}
		if waited >= checksWindow {
			return "", &Fixable{Msg: "tree merge: checks are still pending after the watch: run again"}
		}
		watch = true
		env.wait(checksDelay)
	}
}

// anyPending reports whether a check of pull request pr is still pending. A
// checks read that fails counts as pending: keep watching, do not decide.
func anyPending(gh host.GH, pr int) bool {
	checks, err := gh.Checks(pr)
	if err != nil {
		return true
	}
	for _, c := range checks {
		if strings.EqualFold(c.Bucket, "pending") {
			return true
		}
	}
	return false
}
