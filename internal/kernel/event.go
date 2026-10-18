package kernel

import "time"

// Event kinds.
const (
	// EvTreeOpened may carry Detail["pr"], the decimal number of the pull request
	// the tree merges as; the report reads it as an override of the tree a
	// squash merge "(#N)" is linked to.
	EvTreeOpened   = "tree.opened"
	EvTreeMerged   = "tree.merged"
	EvEdit         = "edit"
	EvTurnAnchored = "turn.anchored"
	EvCommit       = "commit"
	EvMerge        = "merge"
	// EvCIResult carries Detail["result"]; the value "green" ends a deny for the
	// report's net_cost turns.
	EvCIResult = "ci.result"
	// EvSessionUsage carries Detail["transcript"], the path of the session's
	// transcript, and Detail["reason"], why the session ended.
	EvSessionUsage = "session.usage"
	// EvWallDenied carries Detail["row"], the id of the table row that denied,
	// and Detail["bytes"], the decimal length of the line it injected (else
	// Detail["line"], the line itself); the report prices both per row.
	EvWallDenied        = "wall.denied"
	EvWallOverridden    = "wall.overridden"
	EvPolicyDenied      = "policy.denied"
	EvExperimentStarted = "experiment.started"
	EvExperimentEnded   = "experiment.ended"
	// EvInternalError records a failure inside a hook. The hook still exits 0.
	EvInternalError = "internal.error"
)

// Event is one fact in the append-only log. At is supplied by the caller.
type Event struct {
	Seq     uint64            `json:"seq"`
	Kind    string            `json:"kind"`
	Tree    string            `json:"tree"`
	Session string            `json:"session"`
	At      time.Time         `json:"at"`
	Arms    map[string]string `json:"arms,omitempty"`
	Detail  map[string]string `json:"detail,omitempty"`
	// TookMS is how long a hook ran, in whole milliseconds rounded up, from the
	// start of `trellis hook` to just before this event was appended. 0 means
	// not measured: records written before it existed, and events no hook wrote.
	TookMS int64 `json:"took_ms,omitempty"`
}

// Millis is d in whole milliseconds, rounded up so that a hook that took
// something is never recorded as not measured. A d at or below zero is 0.
func Millis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64((d + time.Millisecond - 1) / time.Millisecond)
}
