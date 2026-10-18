package measure

import (
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
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
	f := Facts{Usage: []Usage{{Tree: "A", Session: "s1", Transcript: fixture(t, "transcript_cost.jsonl")}}}

	got := TreeCosts(f)["A"]

	if math.Abs(got-0.0125) > 1e-9 {
		t.Errorf("cost of tree A = %v USD, want 0.0125", got)
	}
}

func TestCost_LineOverTheLimitIsSkippedAndLaterTurnsCount(t *testing.T) {
	rec := func(id string) string {
		return `{"type":"assistant","timestamp":"2026-10-01T09:00:05Z","requestId":"` + id + `","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":5000}}}` + "\n"
	}
	huge := `{"type":"user","message":{"content":"` + strings.Repeat("x", 9<<20) + `"}}` + "\n"
	tr := []byte(rec("a") + huge + rec("b"))

	got := TreeCosts(Facts{Usage: []Usage{{Tree: "A", Transcript: tr}}})["A"]

	if math.Abs(got-0.025) > 1e-9 {
		t.Errorf("cost = %v USD, want 0.025 (both records around the 9 MiB line)", got)
	}
}

func TestCost_UnpricedModelCostsNothingAndIsCounted(t *testing.T) {
	line := func(id, model string, in, out int) string {
		return `{"type":"assistant","timestamp":"2026-10-01T09:00:05Z","requestId":"` + id + `","message":{"model":"` + model + `","usage":{"input_tokens":` + strconv.Itoa(in) + `,"output_tokens":` + strconv.Itoa(out) + `}}}` + "\n"
	}
	f := Facts{Usage: []Usage{
		{Tree: "A", Transcript: []byte(line("q", "mystery-1", 1000000, 1) + line("r", "other-2", 20, 3))},
		{Tree: "B", Transcript: []byte(line("s", "mystery-1", 7, 0))},
	}}

	costs, unpriced := Costs(f)

	if costs["A"] != 0 || costs["B"] != 0 {
		t.Errorf("costs = %v, want 0 for unpriced models", costs)
	}
	if unpriced.Tokens != 1000031 || unpriced.ByTree["A"] != 1000024 || unpriced.ByTree["B"] != 7 {
		t.Errorf("unpriced = %+v, want 1000031 in total, 1000024 in A, 7 in B", unpriced)
	}
	if want := []string{"mystery-1", "other-2"}; !reflect.DeepEqual(unpriced.Models, want) {
		t.Errorf("unpriced models = %v, want %v", unpriced.Models, want)
	}
}

func TestCost_TurnsGoToTheTreeCurrentAtTheNextStop(t *testing.T) {
	rec := func(id, at string) string {
		return `{"type":"assistant","timestamp":"` + at + `","requestId":"` + id + `","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":5000}}}` + "\n"
	}
	tr := []byte(rec("a", "2026-10-01T09:00:05Z") + rec("b", "2026-10-01T09:10:05Z") + rec("c", "2026-10-01T09:30:05Z"))
	marks := []TreeMark{
		{At: time.Date(2026, 10, 1, 9, 5, 0, 0, time.UTC), Tree: "A"},
		{At: time.Date(2026, 10, 1, 9, 20, 0, 0, time.UTC), Tree: "B"},
	}

	costs, _ := Costs(Facts{Usage: []Usage{{Tree: "B", Session: "s", Transcript: tr, Marks: marks}}})

	// a ends at the Stop on A; b at the Stop on B; c came after the last Stop
	// and goes to the last tree.
	if math.Abs(costs["A"]-0.0125) > 1e-9 || math.Abs(costs["B"]-0.025) > 1e-9 {
		t.Errorf("costs = %v, want A 0.0125 and B 0.025", costs)
	}
}

func TestCost_PrefixMustEndAtAFamilyBoundary(t *testing.T) {
	for model, ok := range map[string]bool{
		"claude-opus-5":              true,
		"claude-opus-5-5":            true,
		"claude-opus-5[1m]":          true,
		"claude-opus-5x":             false,
		"claude-opus-50":             false,
		"claude-sonnet-4-6-20260101": true,
	} {
		if _, got := priceFor(model); got != ok {
			t.Errorf("priceFor(%q) found = %v, want %v", model, got, ok)
		}
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
		{Seq: 1, Kind: kernel.EvTreeOpened, Tree: "A", At: at(-60)},
		{Seq: 2, Kind: kernel.EvWallDenied, Tree: "A", At: at(0), Detail: map[string]string{"row": "w.main", "bytes": "400"}},
		{Seq: 3, Kind: kernel.EvCIResult, Tree: "A", At: at(5), Detail: map[string]string{"result": "green"}},
		{Seq: 4, Kind: kernel.EvWallDenied, Tree: "A", At: at(6), Detail: map[string]string{"row": "w.main", "line": "twelve bytes"}},
		{Seq: 5, Kind: kernel.EvWallDenied, Tree: "B", At: at(0), Detail: map[string]string{"row": "w.secrets", "bytes": "40"}},
	}
}

func TestCost_DenyTurnsCharged(t *testing.T) {
	f := Facts{Events: denyEvents(), Usage: []Usage{{Tree: "A", Session: "s2", Transcript: fixture(t, "transcript_deny.jsonl")}}}

	rows := RowCosts(f)

	// Deny at 10:00, green at 10:05: replies r1 (once, though split in two
	// records), r2 and r3 are the 3 turns; r0 is before it and r4 after.
	// The second deny (10:06) has no green: it is unresolved and its turn r4 is
	// reported apart, as UnresolvedTurns.
	// Injected tokens: (400 + 12) / 4 = 103.
	want := []RowCost{
		{Row: "w.main", Denies: 2, InjectedTokens: 103, Turns: 3, Unresolved: 1, UnresolvedTurns: 1},
		{Row: "w.secrets", Denies: 1, InjectedTokens: 10, Turns: 0, Unresolved: 1},
	}
	if len(rows) != 2 || rows[0] != want[0] || rows[1] != want[1] {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}
}

// anchored is n turn.anchored events of tree A, one a minute from 10:01.
func anchored(n int) []kernel.Event {
	var evs []kernel.Event
	for i := 1; i <= n; i++ {
		evs = append(evs, kernel.Event{Kind: kernel.EvTurnAnchored, Tree: "A", At: time.Date(2026, 10, 1, 10, i, 0, 0, time.UTC)})
	}
	return evs
}

func denyAt(m int) kernel.Event {
	return kernel.Event{Kind: kernel.EvWallDenied, Tree: "A", At: time.Date(2026, 10, 1, 10, m, 0, 0, time.UTC), Detail: map[string]string{"row": "r"}}
}

func TestCost_UnresolvedDenyIsCappedAt50Turns(t *testing.T) {
	evs := append([]kernel.Event{denyAt(0)}, anchored(59)...)

	rows := RowCosts(Facts{Events: evs})

	if len(rows) != 1 || rows[0].Turns != 0 || rows[0].UnresolvedTurns != 50 || rows[0].Unresolved != 1 {
		t.Errorf("rows = %+v, want 0 turns and 50 unresolved turns", rows)
	}
}

func TestCost_UnresolvedDenyStopsAtTheNextDeny(t *testing.T) {
	evs := append([]kernel.Event{denyAt(0), denyAt(4)}, anchored(9)...)

	rows := RowCosts(Facts{Events: evs})

	// First deny: turns at 10:01..10:03 (3); the second, at 10:04, takes the
	// turns 10:05..10:09 (5).
	if len(rows) != 1 || rows[0].UnresolvedTurns != 8 || rows[0].Unresolved != 2 {
		t.Errorf("rows = %+v, want 8 unresolved turns over 2 denies", rows)
	}
}

func TestCost_UnresolvedDenyStopsAtTheTreesMerge(t *testing.T) {
	evs := append([]kernel.Event{denyAt(0)}, anchored(9)...)
	merge := Merge{Sha: "aaaaaaa1111", Subject: "x (#1)", PR: 1, Tree: "A", At: time.Date(2026, 10, 1, 10, 3, 0, 0, time.UTC)}

	rows := RowCosts(Facts{Events: evs, Merges: []Merge{merge}})

	// The merge at 10:03 ends it: turns 10:01, 10:02 and 10:03.
	if len(rows) != 1 || rows[0].UnresolvedTurns != 3 {
		t.Errorf("rows = %+v, want 3 unresolved turns", rows)
	}
}

func TestCost_DenyTurnsFallBackToAnchoredEvents(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 10, 1, 10, m, 0, 0, time.UTC) }
	evs := []kernel.Event{
		{Kind: kernel.EvWallDenied, Tree: "A", At: at(0), Detail: map[string]string{"row": "r"}},
		{Kind: kernel.EvTurnAnchored, Tree: "A", At: at(1)},
		{Kind: kernel.EvTurnAnchored, Tree: "A", At: at(2)},
		{Kind: kernel.EvCIResult, Tree: "A", At: at(3), Detail: map[string]string{"result": "green"}},
		{Kind: kernel.EvTurnAnchored, Tree: "A", At: at(4)},
	}
	rows := RowCosts(Facts{Events: evs})
	if len(rows) != 1 || rows[0].Turns != 2 || rows[0].Unresolved != 0 {
		t.Errorf("rows = %+v, want 2 turns resolved", rows)
	}
}
