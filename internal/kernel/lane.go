package kernel

// LaneState is the lane machine: none -> open -> merged | abandoned.
type LaneState int

// The lane states; a lane moves forward only.
const (
	LaneNone LaneState = iota
	LaneOpen
	LaneMerged
	// LaneAbandoned is set by the report's history fold (lane 7: a branch
	// deleted without a merge), never by a hook and never by Advance, as with
	// lane.merged. Advance turns an abandoned lane into merged on lane.merged.
	LaneAbandoned
)

func (s LaneState) String() string {
	switch s {
	case LaneNone:
		return "none"
	case LaneOpen:
		return "open"
	case LaneMerged:
		return "merged"
	case LaneAbandoned:
		return "abandoned"
	}
	return "unknown"
}

// Advance folds one event into a lane's state. A lane opens at its first
// event. lane.merged comes from the report's history fold, never a hook. An
// event for a merged lane is kept and flagged Detail["after"]="merged"; the
// caller's Detail map is not mutated.
func Advance(s LaneState, ev Event) (LaneState, Event) {
	switch {
	case ev.Kind == EvLaneMerged:
		return LaneMerged, ev
	case s == LaneNone:
		return LaneOpen, ev
	case s == LaneMerged:
		d := make(map[string]string, len(ev.Detail)+1)
		for k, v := range ev.Detail {
			d[k] = v
		}
		d[DetailAfter] = "merged"
		ev.Detail = d
	}
	return s, ev
}
