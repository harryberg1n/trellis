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

// Context carries the parsed tool call. Step 6 fills it.
type Context struct {
	Tool string
	Args map[string]string
}

type matcher func(Row, Event, Context) bool

// noMatch is the step 2 matcher: matching arrives in step 6.
func noMatch(Row, Event, Context) bool { return false }

// Decide is the pure rule evaluation. Until step 6 only the state and arm
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
// live-ab rows only for trees in the treatment arm; candidate, replay and cut
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

// Matcher judges one row on one tool call. It returns the row's action and
// true, or false when the row has nothing to say about the call. The verdict it
// returns is what the row would do at Level Block; DecideWith caps it at a warn
// for a row at Level Warn.
type Matcher func(Row, Event, Context) (Action, bool)

// DecideWith is Decide with a matcher: the rows that act on ev, in table order,
// each asked about ctx. The matcher is how effects (a process, a file read)
// reach the rule evaluation without the kernel owning them.
func DecideWith(rows []Row, ev Event, ctx Context, m Matcher) []Action {
	var out []Action
	for _, r := range rows {
		if !acts(r, ev) {
			continue
		}
		a, ok := m(r, ev, ctx)
		if !ok {
			continue
		}
		a.RowID = r.ID
		if r.Level == Warn && a.Verdict == VerdictDeny {
			a.Verdict = VerdictWarn
		}
		out = append(out, a)
	}
	return out
}
