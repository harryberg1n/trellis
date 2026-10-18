package measure

import (
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func day(n int) time.Time { return t0.Add(time.Duration(n) * 24 * time.Hour) }

func span(s, e int) []Span { return []Span{{Start: s, End: e}} }

func TestLabels_FourLaneFixture(t *testing.T) {
	f := Facts{
		Now: day(20),
		Merges: []Merge{
			{Sha: "aaaaaaa1111", Subject: "add a (#1)", PR: 1, Lane: "A", At: day(0), Added: map[string][]Span{"a.go": span(1, 10)}},
			{Sha: "bbbbbbb2222", Subject: "add b (#2)", PR: 2, Lane: "B", At: day(0), Added: map[string][]Span{"b.go": span(1, 10)}},
			{Sha: "ccccccc3333", Subject: "add c (#3)", PR: 3, Lane: "C", At: day(0), Added: map[string][]Span{"c.go": span(1, 10)}},
			{Sha: "ddddddd4444", Subject: "add d (#4)", PR: 4, Lane: "D", At: day(15), Added: map[string][]Span{"d.go": span(1, 10)}},
			// A is reverted on day 2.
			{Sha: "eeeeeee5555", Subject: "Revert \"add a\"", Body: "This reverts commit aaaaaaa1111.", At: day(2)},
			// B is fixed on day 15: outside its window.
			{Sha: "fffffff6666", Subject: "fix: b (#6)", PR: 6, At: day(15), Removed: map[string][]Span{"b.go": span(5, 6)}},
			// C is overlapped by an ordinary merge on day 3.
			{Sha: "1111111aaaa", Subject: "extend c (#7)", PR: 7, At: day(3), Removed: map[string][]Span{"c.go": span(5, 6)}},
		},
	}

	got := Labels(f)

	want := map[string]Label{"A": Defect, "B": Clean, "C": Clean, "D": Pending}
	for lane, w := range want {
		if got[lane] != w {
			t.Errorf("lane %s = %q, want %q", lane, got[lane], w)
		}
	}
}

func TestLabels_FixOnItsLinesInsideTheWindowIsADefect(t *testing.T) {
	f := Facts{Now: day(30), Merges: []Merge{
		{Sha: "bbbbbbb2222", Subject: "add b (#2)", PR: 2, Lane: "B", At: day(0), Added: map[string][]Span{"b.go": span(1, 10)}},
		{Sha: "fffffff6666", Subject: "Fix b (#6)", PR: 6, At: day(13), Removed: map[string][]Span{"b.go": span(10, 12)}},
	}}
	if got := Labels(f)["B"]; got != Defect {
		t.Errorf("lane B = %q, want %q (a fix touching line 10)", got, Defect)
	}
}

func TestLabels_FixOnOtherLinesIsNotADefect(t *testing.T) {
	f := Facts{Now: day(30), Merges: []Merge{
		{Sha: "bbbbbbb2222", Subject: "add b (#2)", PR: 2, Lane: "B", At: day(0), Added: map[string][]Span{"b.go": span(1, 10)}},
		{Sha: "fffffff7777", Subject: "fix elsewhere (#8)", PR: 8, At: day(3), Removed: map[string][]Span{"b.go": span(11, 12), "z.go": span(1, 2)}},
	}}
	if got := Labels(f)["B"]; got != Clean {
		t.Errorf("lane B = %q, want %q", got, Clean)
	}
}

func TestLabels_CommitNamingThePRIsADefect(t *testing.T) {
	f := Facts{Now: day(30), Merges: []Merge{
		{Sha: "bbbbbbb2222", Subject: "add b (#12)", PR: 12, Lane: "B", At: day(0)},
		{Sha: "ccccccc3333", Subject: "tidy (#123)", PR: 123, At: day(1)},
		{Sha: "ddddddd4444", Subject: "follow-up to #12 (#13)", PR: 13, At: day(2)},
	}}
	if got := Labels(f)["B"]; got != Defect {
		t.Errorf("lane B = %q, want %q", got, Defect)
	}
}

func TestLabels_NumberPrefixIsNotAName(t *testing.T) {
	f := Facts{Now: day(30), Merges: []Merge{
		{Sha: "bbbbbbb2222", Subject: "add b (#12)", PR: 12, Lane: "B", At: day(0)},
		{Sha: "ccccccc3333", Subject: "tidy (#123)", PR: 123, At: day(1)},
	}}
	if got := Labels(f)["B"]; got != Clean {
		t.Errorf("lane B = %q, want %q", got, Clean)
	}
}

func TestLabels_EscapeIssueNamingThePR(t *testing.T) {
	f := Facts{Now: day(30), Merges: []Merge{
		{Sha: "bbbbbbb2222", Subject: "add b (#12)", PR: 12, Lane: "B", At: day(0)},
		{Sha: "ccccccc3333", Subject: "add c (#13)", PR: 13, Lane: "C", At: day(0)},
	}, Issues: []Issue{
		{Number: 40, Title: "escape: login breaks", Body: "introduced by #12", At: day(4)},
		{Number: 41, Title: "docs typo mentions #13", Body: "", At: day(4)},
	}}
	got := Labels(f)
	if got["B"] != Defect || got["C"] != Clean {
		t.Errorf("labels = %v, want B defect and C clean", got)
	}
}

func TestLabels_EscapeIssueAfterTheWindowIsIgnored(t *testing.T) {
	f := Facts{Now: day(60), Merges: []Merge{
		{Sha: "bbbbbbb2222", Subject: "add b (#12)", PR: 12, Lane: "B", At: day(0)},
	}, Issues: []Issue{{Number: 40, Title: "escape: late", Body: "#12", At: day(20)}}}
	if got := Labels(f)["B"]; got != Clean {
		t.Errorf("lane B = %q, want clean", got)
	}
}

func TestLabels_OverlapAloneIsOff(t *testing.T) {
	f := Facts{Now: day(60), Merges: []Merge{
		{Sha: "bbbbbbb2222", Subject: "add b (#2)", PR: 2, Lane: "B", At: day(0), Added: map[string][]Span{"b.go": span(1, 10)}},
		{Sha: "ccccccc3333", Subject: "rework (#3)", PR: 3, At: day(1), Removed: map[string][]Span{"b.go": span(1, 10)}},
	}}
	if got := Labels(f)["B"]; got != Clean {
		t.Errorf("lane B = %q, want clean", got)
	}
}

func TestLabels_LaneWithoutNameIsKeyedByPR(t *testing.T) {
	f := Facts{Now: day(60), Merges: []Merge{{Sha: "bbbbbbb2222", Subject: "x (#9)", PR: 9, At: day(0)}}}
	want := map[string]Label{"pr-9": Clean}
	if got := Labels(f); !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
}

func TestParseHunks(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -3,2 +3,4 @@ func x\n+x\n@@ -20 +22,0 @@\n" +
		"diff --git a/gone.go b/gone.go\n--- a/gone.go\n+++ /dev/null\n@@ -1,5 +0,0 @@\n"
	added, removed := ParseHunks(diff)
	wa := map[string][]Span{"a.go": {{Start: 3, End: 6}}}
	wr := map[string][]Span{"a.go": {{Start: 3, End: 4}, {Start: 20, End: 20}}, "gone.go": {{Start: 1, End: 5}}}
	if !reflect.DeepEqual(added, wa) || !reflect.DeepEqual(removed, wr) {
		t.Errorf("added %v removed %v, want %v and %v", added, removed, wa, wr)
	}
}
