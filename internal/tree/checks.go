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
// first one, checksDelay between reads.
const (
	checksWindow = 90 * time.Second
	checksDelay  = 5 * time.Second
)

// awaitChecks polls the checks of pull request pr until one exists, and
// reports whether one did. None within checksWindow is accepted only when the
// repository defines no workflows or GitHub calls the pull request CLEAN; any
// other merge state is a Fixable, since checks may still be on their way.
func awaitChecks(env Env, gh host.GH, pr int, branch string) (bool, error) {
	noWorkflows := false
	for waited := time.Duration(0); ; waited += checksDelay {
		checks, err := gh.Checks(pr)
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
		if waited >= checksWindow {
			break
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

// watchAndReady waits for the checks to finish, then judges the pull request.
// A merge state of UNSTABLE or BLOCKED while a check is still pending is not a
// verdict: GitHub counted a check the watch had not seen, so watch again, for
// at most checksWindow of waiting. watch is false when the pull request has
// no check to watch.
func watchAndReady(env Env, gh host.GH, repo objects.Repo, branch string, pr int, watch bool) error {
	for waited := time.Duration(0); ; waited += checksDelay {
		if watch {
			switch _, err := gh.WatchChecks(pr); {
			case errors.Is(err, host.ErrNoChecks):
			case err != nil:
				return checksError(env, gh, pr, err)
			default:
				env.say("checks passed on #%d", pr)
			}
		}
		pending, err := readyToMerge(env, gh, repo, branch)
		if err != nil {
			return err
		}
		if !pending {
			return nil
		}
		if waited >= checksWindow {
			return &Fixable{Msg: "tree merge: checks are still pending after the watch: run again"}
		}
		watch = true
		env.wait(checksDelay)
	}
}

// anyPending reports whether a check of pull request pr is still pending.
func anyPending(gh host.GH, pr int) bool {
	checks, _ := gh.Checks(pr)
	for _, c := range checks {
		if strings.EqualFold(c.Bucket, "pending") {
			return true
		}
	}
	return false
}
