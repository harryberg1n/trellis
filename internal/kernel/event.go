package kernel

import "time"

// Event kinds.
const (
	EvLaneOpened        = "lane.opened"
	EvLaneMerged        = "lane.merged"
	EvEdit              = "edit"
	EvTurnAnchored      = "turn.anchored"
	EvCommit            = "commit"
	EvMerge             = "merge"
	EvCIResult          = "ci.result"
	EvSessionUsage      = "session.usage"
	EvWallDenied        = "wall.denied"
	EvWallOverridden    = "wall.overridden"
	EvPolicyDenied      = "policy.denied"
	EvExperimentStarted = "experiment.started"
	EvExperimentEnded   = "experiment.ended"
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
