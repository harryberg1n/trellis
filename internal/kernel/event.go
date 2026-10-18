package kernel

import "time"

// Event kinds.
const (
	// EvLaneOpened may carry Detail["pr"], the decimal number of the pull request
	// the lane merges as; the report reads it as an override of the lane a
	// squash merge "(#N)" is linked to.
	EvLaneOpened   = "lane.opened"
	EvLaneMerged   = "lane.merged"
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
	Lane    string            `json:"lane"`
	Session string            `json:"session"`
	At      time.Time         `json:"at"`
	Arms    map[string]string `json:"arms,omitempty"`
	Detail  map[string]string `json:"detail,omitempty"`
}
