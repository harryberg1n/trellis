package kernel

// Verdict is what a row decides about one event: let it pass, deny it, or let
// it pass with a warning line for the agent.
type Verdict int

// The verdicts. Deny comes only from Block rows.
const (
	VerdictAllow Verdict = iota
	VerdictDeny
	VerdictWarn
)

func (v Verdict) String() string {
	switch v {
	case VerdictAllow:
		return "allow"
	case VerdictDeny:
		return "deny"
	case VerdictWarn:
		return "warn"
	}
	return "unknown"
}

// Action is one row's verdict on one event.
type Action struct {
	RowID   string
	Verdict Verdict
	Line    string
}

// Context carries the parsed tool call. Lane 6 fills it.
type Context struct {
	Tool string
	Args map[string]string
}

type matcher func(Row, Event, Context) bool

// noMatch is the lane-2 matcher: matching arrives in lane 6.
func noMatch(Row, Event, Context) bool { return false }

// Decide is the pure rule evaluation. In this lane only the state and arm
// filters exist, and nothing matches yet.
func Decide(rows []Row, ev Event, ctx Context) []Action {
	return decide(rows, ev, ctx, noMatch)
}

func decide(rows []Row, ev Event, ctx Context, match matcher) []Action {
	var out []Action
	for _, r := range rows {
		if !acts(r, ev) || !match(r, ev, ctx) {
			continue
		}
		v := VerdictWarn
		if r.Level == Block {
			v = VerdictDeny
		}
		out = append(out, Action{RowID: r.ID, Verdict: v, Line: r.ID + ": " + v.String()})
	}
	return out
}

// acts reports whether the row may act on the event: admitted rows always,
// live-ab rows only for lanes in the treatment arm; candidate, replay and cut
// rows never.
func acts(r Row, ev Event) bool {
	switch r.State {
	case Admitted:
		return true
	case LiveAB:
		return ev.Arms[r.Experiment] == ArmTreatment
	}
	return false
}
