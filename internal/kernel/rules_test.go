package kernel

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

func loadEvents(t *testing.T) []Event {
	t.Helper()
	f, err := os.Open("testdata/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }() // read-only
	var evs []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return evs
}

func matchAll(Row, Event, Context) bool { return true }

func TestDecide_CandidateAndReplayRowsNeverAct(t *testing.T) {
	evs := loadEvents(t)
	if len(evs) != 50 {
		t.Fatalf("fixture has %d events, want 50", len(evs))
	}
	rows := []Row{{ID: "cand", State: Candidate, Level: Block, Decision: "d"}, {ID: "rep", State: Replay}}
	n := 0
	for _, ev := range evs {
		n += len(decide(rows, ev, Context{}, matchAll))
	}
	if n != 0 {
		t.Errorf("actions = %d, want 0", n)
	}
}

func TestDecide_AdmittedRowsActAndLiveABFollowsTheArm(t *testing.T) {
	rows := []Row{
		{ID: "adm", State: Admitted},
		{ID: "blk", State: Admitted, Level: Block, Decision: "d"},
		{ID: "ab", State: LiveAB, Experiment: "exp1"},
		{ID: "cut", State: Cut},
	}
	got := decide(rows, Event{Arms: map[string]string{"exp1": "control"}}, Context{}, matchAll)
	want := []Action{{"adm", VerdictWarn, "adm: warn"}, {"blk", VerdictDeny, "blk: deny"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("control: %+v, want %+v", got, want)
	}
	got = decide(rows, Event{Arms: map[string]string{"exp1": "treatment"}}, Context{}, matchAll)
	if len(got) != 3 || got[2].RowID != "ab" {
		t.Errorf("treatment: %+v, want adm, blk, ab", got)
	}
}

func TestDecide_PublicDecideMatchesNothingYet(t *testing.T) {
	if got := Decide([]Row{{ID: "adm", State: Admitted}}, Event{}, Context{}); len(got) != 0 {
		t.Errorf("actions = %+v, want none until lane 6 adds matching", got)
	}
}
