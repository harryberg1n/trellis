package measure

import (
	"os"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCost_FixtureTranscript(t *testing.T) {
	f := Facts{Usage: []Usage{{Lane: "A", Session: "s1", Transcript: fixture(t, "transcript_cost.jsonl")}}}

	got := LaneCosts(f)["A"]

	if got != 0.0125 {
		t.Errorf("cost of lane A = %v USD, want 0.0125", got)
	}
}

func TestCost_UnpricedModelCostsNothing(t *testing.T) {
	tr := []byte(`{"type":"assistant","timestamp":"2026-10-01T09:00:05Z","requestId":"q","message":{"model":"mystery-1","usage":{"input_tokens":1000000,"output_tokens":1}}}` + "\n")
	if got := LaneCosts(Facts{Usage: []Usage{{Lane: "A", Transcript: tr}}})["A"]; got != 0 {
		t.Errorf("cost = %v, want 0", got)
	}
}

func TestCost_LongestPrefixWins(t *testing.T) {
	if p, ok := priceFor("claude-sonnet-5-20260101[1m]"); !ok || p.Input != 3 {
		t.Errorf("price = %+v %v, want input 3", p, ok)
	}
}

func denyEvents() []kernel.Event {
	at := func(m int) time.Time { return time.Date(2026, 10, 1, 10, m, 0, 0, time.UTC) }
	return []kernel.Event{
		{Seq: 1, Kind: kernel.EvLaneOpened, Lane: "A", At: at(-60)},
		{Seq: 2, Kind: kernel.EvWallDenied, Lane: "A", At: at(0), Detail: map[string]string{"row": "w.main", "bytes": "400"}},
		{Seq: 3, Kind: kernel.EvCIResult, Lane: "A", At: at(5), Detail: map[string]string{"result": "green"}},
		{Seq: 4, Kind: kernel.EvWallDenied, Lane: "A", At: at(6), Detail: map[string]string{"row": "w.main", "line": "twelve bytes"}},
		{Seq: 5, Kind: kernel.EvWallDenied, Lane: "B", At: at(0), Detail: map[string]string{"row": "w.secrets", "bytes": "40"}},
	}
}

func TestCost_DenyTurnsCharged(t *testing.T) {
	f := Facts{Events: denyEvents(), Usage: []Usage{{Lane: "A", Session: "s2", Transcript: fixture(t, "transcript_deny.jsonl")}}}

	rows := RowCosts(f)

	// Deny at 10:00, green at 10:05: replies r1 (once, though split in two
	// records), r2 and r3 are the 3 turns; r0 is before it and r4 after.
	// The second deny (10:06) has no green: it is unresolved and takes r4.
	// Injected tokens: (400 + 12) / 4 = 103.
	want := []RowCost{
		{Row: "w.main", Denies: 2, InjectedTokens: 103, Turns: 4, Unresolved: 1},
		{Row: "w.secrets", Denies: 1, InjectedTokens: 10, Turns: 0, Unresolved: 1},
	}
	if len(rows) != 2 || rows[0] != want[0] || rows[1] != want[1] {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}
}

func TestCost_DenyTurnsFallBackToAnchoredEvents(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 10, 1, 10, m, 0, 0, time.UTC) }
	evs := []kernel.Event{
		{Kind: kernel.EvWallDenied, Lane: "A", At: at(0), Detail: map[string]string{"row": "r"}},
		{Kind: kernel.EvTurnAnchored, Lane: "A", At: at(1)},
		{Kind: kernel.EvTurnAnchored, Lane: "A", At: at(2)},
		{Kind: kernel.EvCIResult, Lane: "A", At: at(3), Detail: map[string]string{"result": "green"}},
		{Kind: kernel.EvTurnAnchored, Lane: "A", At: at(4)},
	}
	rows := RowCosts(Facts{Events: evs})
	if len(rows) != 1 || rows[0].Turns != 2 || rows[0].Unresolved != 0 {
		t.Errorf("rows = %+v, want 2 turns resolved", rows)
	}
}
