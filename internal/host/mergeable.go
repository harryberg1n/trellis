package host

// Copied from aphrollo-tools internal/workspace/conflict.go at 12b3724e
// (mergeablePollAttempts, mergeablePollDelay, isConflicting, mergeUnknown,
// viewPRMergeable). Incident: GitHub computes `mergeable` asynchronously, so
// it reads UNKNOWN for a short window after a push, and a merge that took
// UNKNOWN for clean (or for a conflict) misjudged the branch. What changed:
// the PR is read through GH.PRView, the bounds are constants, and the delay is
// a wait function the caller takes from its clock, so a test sleeps nothing.

import (
	"strings"
	"time"
)

// mergeablePollAttempts and mergeablePollDelay bound the re-poll GitHub forces
// on us. The bound is deliberately small: this runs inside a live turn.
const (
	mergeablePollAttempts = 4
	mergeablePollDelay    = 750 * time.Millisecond
)

// IsConflicting reports whether GitHub has determined the branch cannot merge
// cleanly: mergeable==CONFLICTING or the finer mergeStateStatus==DIRTY. Both
// are matched case-insensitively. UNKNOWN is NOT a conflict: it is "not
// computed yet" (see MergeUnknown).
func (p PRState) IsConflicting() bool {
	return strings.EqualFold(p.Mergeable, "CONFLICTING") ||
		strings.EqualFold(p.MergeStateStatus, "DIRTY")
}

// MergeUnknown reports whether GitHub has NOT yet computed mergeability. An
// empty string is not unknown: only the literal UNKNOWN gh returns is.
func (p PRState) MergeUnknown() bool {
	return strings.EqualFold(p.Mergeable, "UNKNOWN")
}

// IsBehind reports whether the base has moved on and the branch protection
// wants the branch brought up to date first.
func (p PRState) IsBehind() bool {
	return strings.EqualFold(p.MergeStateStatus, "BEHIND")
}

// ViewMergeable reads the pull request of branch and, while GitHub still
// reports mergeable as UNKNOWN, re-polls a bounded number of times, calling
// wait with the delay between reads. If it stays UNKNOWN within the bound, the
// last (UNKNOWN) state is returned so the caller does not take it for an
// all-clear. An error wrapping ErrNoPR when the branch has no pull request.
func (g GH) ViewMergeable(branch string, wait func(time.Duration)) (PRState, error) {
	var pr PRState
	for i := 0; i < mergeablePollAttempts; i++ {
		var err error
		pr, err = g.PRView(branch)
		if err != nil {
			return PRState{}, err
		}
		if !pr.MergeUnknown() {
			return pr, nil
		}
		if i < mergeablePollAttempts-1 {
			wait(mergeablePollDelay)
		}
	}
	return pr, nil
}
