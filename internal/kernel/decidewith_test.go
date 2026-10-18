package kernel

import (
	"reflect"
	"testing"
)

func TestDecideWith_RunsTheMatcherOnActingRowsOnly(t *testing.T) {
	rows := []Row{
		{ID: "a", State: Admitted, Level: Block},
		{ID: "b", State: Candidate, Level: Block},
		{ID: "c", State: Admitted, Level: Warn},
		{ID: "d", State: Admitted, Level: Block},
	}
	m := func(r Row, _ Event, _ Context) (Action, bool) {
		if r.ID == "d" {
			return Action{}, false
		}
		return Action{Verdict: VerdictDeny, Line: "line " + r.ID}, true
	}
	got := DecideWith(rows, Event{}, Context{}, m)
	want := []Action{
		{RowID: "a", Verdict: VerdictDeny, Line: "line a"},
		{RowID: "c", Verdict: VerdictWarn, Line: "line c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DecideWith = %+v, want %+v (b is not admitted, c is a warn row, d did not match)", got, want)
	}
}
