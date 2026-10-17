package kernel

import (
	"fmt"
	"strings"
	"time"
)

type Kind int

const (
	Check Kind = iota
	Behaviour
	Wall
	Policy
)

type Stage int

const (
	StageEdit Stage = iota
	StageCommit
	StageMerge
	StageSession
)

// State is the row's admission state. Only Admitted and LiveAB rows act.
type State int

const (
	Candidate State = iota
	Replay
	LiveAB
	Admitted
	Cut
)

type Level int

const (
	Warn Level = iota
	Block
)

// Class is the cost class of a row. The run package does not exist yet, so it
// is defined here; move it there when it does.
type Class int

const (
	Light Class = iota
	Heavy
	Bulk
)

// Caps is a bit set of ecosystem capabilities.
type Caps uint32

const (
	Tests Caps = 1 << iota
	Select
	Mutation
	Lint
	Typecheck
	Doctest
	Coverage
)

// Row is one entry of the feature table.
type Row struct {
	ID         string
	Kind       Kind   // Check | Behaviour | Wall | Policy
	Stage      Stage  // edit | commit | merge | session
	Needs      Caps   // ecosystem capabilities it requires
	State      State  // candidate | replay | live-ab | admitted | cut
	Level      Level  // warn | block (block only with a may-block decision and a pin)
	Experiment string // for live-ab rows
	Incident   string // required for Wall rows
	// Decision is required for Policy rows, and for Block rows it is the
	// may-block decision with its owner pin ("<date> <owner>").
	Decision string
	Budget   time.Duration
	Class    Class
}

// Validate reports every way the table breaks its own rules.
func Validate(rows []Row) []error {
	var errs []error
	seen := map[string]bool{}
	for i, r := range rows {
		if r.ID == "" {
			errs = append(errs, fmt.Errorf("row %d: empty ID", i))
		} else if seen[r.ID] {
			errs = append(errs, fmt.Errorf("%s: duplicate row ID", r.ID))
		}
		seen[r.ID] = true
		if r.Kind == Wall && r.Incident == "" {
			errs = append(errs, fmt.Errorf("%s: wall row needs Incident", r.ID))
		}
		if r.Kind == Policy && r.Decision == "" {
			errs = append(errs, fmt.Errorf("%s: policy row needs Decision", r.ID))
		}
		if r.Level == Block && r.Kind != Policy && r.Decision == "" {
			errs = append(errs, fmt.Errorf("%s: block needs a may-block decision and an owner pin", r.ID))
		}
		if r.Decision != "" && !decisionOK(r.Decision) {
			errs = append(errs, fmt.Errorf("%s: Decision must be \"<YYYY-MM-DD> <owner>\", got %q", r.ID, r.Decision))
		}
		if r.State == LiveAB && r.Experiment == "" {
			errs = append(errs, fmt.Errorf("%s: live-ab row needs Experiment", r.ID))
		}
	}
	return errs
}

// decisionOK reports whether d has the form "<YYYY-MM-DD> <owner>": a date
// parsable with 2006-01-02, one space, and an owner with no spaces.
func decisionOK(d string) bool {
	date, owner, ok := strings.Cut(d, " ")
	if !ok || owner == "" || strings.ContainsAny(owner, " \t\r\n") {
		return false
	}
	_, err := time.Parse("2006-01-02", date)
	return err == nil
}
