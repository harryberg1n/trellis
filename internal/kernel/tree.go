package kernel

// TreeState is the tree machine: none -> open -> merged | abandoned.
type TreeState int

// The tree states; a tree moves forward only.
const (
	TreeNone TreeState = iota
	TreeOpen
	TreeMerged
	// TreeAbandoned is set by the report's history fold (step 7: a branch
	// deleted without a merge), never by a hook and never by Advance, as with
	// tree.merged. Advance turns an abandoned tree into merged on tree.merged.
	TreeAbandoned
)

func (s TreeState) String() string {
	switch s {
	case TreeNone:
		return "none"
	case TreeOpen:
		return "open"
	case TreeMerged:
		return "merged"
	case TreeAbandoned:
		return "abandoned"
	}
	return "unknown"
}

// Advance folds one event into a tree's state. A tree opens at its first
// event. tree.merged comes from the report's history fold, never a hook. An
// event for a merged tree is kept and flagged Detail["after"]="merged"; the
// caller's Detail map is not mutated.
func Advance(s TreeState, ev Event) (TreeState, Event) {
	switch {
	case ev.Kind == EvTreeMerged:
		return TreeMerged, ev
	case s == TreeNone:
		return TreeOpen, ev
	case s == TreeMerged:
		d := make(map[string]string, len(ev.Detail)+1)
		for k, v := range ev.Detail {
			d[k] = v
		}
		d[DetailAfter] = "merged"
		ev.Detail = d
	}
	return s, ev
}
