package measure

import (
	"reflect"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/kernel"
)

func anchoredSplit(anchorMS, gitMS string) kernel.Event {
	return kernel.Event{Kind: kernel.EvTurnAnchored, TookMS: 1, Detail: map[string]string{"anchor_ms": anchorMS, "git_ms": gitMS}}
}

func splitEvents() []kernel.Event {
	return []kernel.Event{
		anchoredSplit("100", "status=60/1 mktree=20/2"),
		anchoredSplit("200", "status=150/1"),
		anchoredSplit("300", "mktree=60/4 status=200/1"),
		{Kind: kernel.EvTurnAnchored, TookMS: 900},                                                            // written before the split existed
		anchoredSplit("50", "status=sixty/1"),                                                                 // unreadable: left out whole
		{Kind: kernel.EvEdit, TookMS: 3, Detail: map[string]string{"anchor_ms": "9", "git_ms": "status=9/1"}}, // not a Stop
	}
}

func TestStopSplit_PartsOverTheAnchorsThatCarryOne(t *testing.T) {
	got := StopSplit(splitEvents())
	want := []SplitPart{
		{Name: "anchor", N: 3, P50: 200, P95: 300},
		{Name: "status", N: 3, P50: 150, P95: 200, Runs50: 1, Runs95: 1},
		{Name: "mktree", N: 3, P50: 20, P95: 60, Runs50: 2, Runs95: 4},
		{Name: "rest", N: 3, P50: 40, P95: 50},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StopSplit =\n%+v\nwant\n%+v", got, want)
	}
}

func TestStopSplit_NoneWhenNoAnchorCarriesOne(t *testing.T) {
	if got := StopSplit([]kernel.Event{{Kind: kernel.EvTurnAnchored, TookMS: 900}}); got != nil {
		t.Errorf("StopSplit = %+v, want nil", got)
	}
}

func TestReport_StopSplitBlockFollowsTheLatencyLines(t *testing.T) {
	got := string(Report(Facts{Now: day(20), Events: splitEvents()}))
	want := "  Stop                     p50 1    p95 900  n 5     (criterion 300: over)\n" +
		"  Stop anchor over 3 turns, p50 / p95 ms (a git child by kind, with how many ran per turn; rest is hashing and reading files)\n" +
		"    anchor                 p50 200  p95 300\n" +
		"    status                 p50 150  p95 200  runs 1 / 1\n" +
		"    mktree                 p50 20   p95 60   runs 2 / 4\n" +
		"    rest                   p50 40   p95 50\n" +
		"\nas of "
	if !strings.Contains(got, want) {
		t.Fatalf("report lacks the block\n--- want\n%s\n--- got\n%s", want, got)
	}
}

func TestReport_NoStopSplitBlockWithoutOne(t *testing.T) {
	got := string(Report(Facts{Now: day(20), Events: tooks(kernel.EvTurnAnchored, 40, 180)}))
	if strings.Contains(got, "Stop anchor over") {
		t.Fatalf("report has a split block with no split recorded:\n%s", got)
	}
}
