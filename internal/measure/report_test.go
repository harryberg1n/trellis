package measure

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
)

var update = flag.Bool("update", false, "rewrite testdata/golden_report.txt")

// reportFixture is four lanes, one of them with a reverted merge and agent
// usage, with the paths and transcript text a report must never repeat.
func reportFixture(t *testing.T) Facts {
	t.Helper()
	opened := func(lane string, at time.Time) kernel.Event {
		return kernel.Event{Kind: kernel.EvLaneOpened, Lane: lane, At: at}
	}
	evs := append(denyEvents(),
		opened("B", day(0).Add(-30*time.Hour)),
		opened("C", day(0).Add(-5*time.Hour)),
		opened("D", day(15).Add(-2*time.Hour)))
	evs[0].At = day(0).Add(-3 * time.Hour) // lane A opened 3 hours before its merge
	return Facts{
		Now: day(20),
		Merges: []Merge{
			{Sha: "aaaaaaa1111", Subject: "add a (#1)", PR: 1, Lane: "A", At: day(0), Added: map[string][]Span{"secret/path/a.go": span(1, 10)}},
			{Sha: "bbbbbbb2222", Subject: "add b (#2)", PR: 2, Lane: "B", At: day(0)},
			{Sha: "ccccccc3333", Subject: "add c (#3)", PR: 3, Lane: "C", At: day(0)},
			{Sha: "ddddddd4444", Subject: "add d (#4)", PR: 4, Lane: "D", At: day(15)},
			{Sha: "eeeeeee5555", Subject: "Revert \"add a\"", Body: "This reverts commit aaaaaaa1111.", At: day(2)},
		},
		Events: evs,
		Usage: []Usage{
			{Lane: "A", Session: "s1", Transcript: fixture(t, "transcript_cost.jsonl")},
			{Lane: "A", Session: "s2", Transcript: fixture(t, "transcript_deny.jsonl")},
		},
	}
}

func TestReport_GoldenBytes(t *testing.T) {
	f := reportFixture(t)

	first := Report(f)
	second := Report(f)

	if !bytes.Equal(first, second) {
		t.Fatalf("two runs differ:\n%s\n---\n%s", first, second)
	}
	if *update {
		if err := os.WriteFile("testdata/golden_report.txt", first, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Skip("golden updated; review the diff")
	}
	want := fixture(t, "golden_report.txt")
	if !bytes.Equal(first, want) {
		t.Errorf("report differs from testdata/golden_report.txt:\n%s\n--- want\n%s", first, want)
	}
}

func TestReport_AggregatesOnly(t *testing.T) {
	out := string(Report(reportFixture(t)))
	for _, bad := range []string{"secret/path", "a.go", "SENTINEL", "s1", "s2", "aaaaaaa", "add a"} {
		if strings.Contains(out, bad) {
			t.Errorf("report contains %q:\n%s", bad, out)
		}
	}
}
