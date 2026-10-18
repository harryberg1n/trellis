package measure

import (
	"strconv"
	"strings"
	"testing"
)

func TestIntervalRanks_NearestRankOfTheFifthAndNinetyFifthPercentile(t *testing.T) {
	lo, hi := intervalRanks(2000)

	if lo != 99 || hi != 1899 {
		t.Errorf("ranks = %d, %d, want 99, 1899", lo, hi)
	}
}

func TestReport_NoIntervalBelowTheEvidenceFloor(t *testing.T) {
	out := string(Report(reportFixture(t)))

	for _, want := range []string{
		"  mean 600.0 minutes, n=4, no interval\n",
		"  per tree: mean $0.01405, n=1, no interval\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "90% interval") {
		t.Errorf("an interval over fewer than 10 samples:\n%s", out)
	}
}

func TestReport_NoSamplesSaysNZero(t *testing.T) {
	out := string(Report(Facts{Now: day(20)}))

	for _, want := range []string{
		"  mean 0.0 minutes, n=0, no interval\n",
		"  per tree: mean $0.00000, n=0, no interval\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestReport_TenSamplesGetTheInterval(t *testing.T) {
	var f Facts
	f.Now = day(20)
	for i := range 10 {
		tree := "T" + strconv.Itoa(i)
		f.Usage = append(f.Usage, Usage{Tree: tree, Transcript: []byte(
			`{"type":"assistant","timestamp":"2026-10-01T09:00:05Z","requestId":"q","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1000000,"output_tokens":0}}}` + "\n")})
	}

	out := string(Report(f))

	want := "  per tree: mean $5.00000, 90% interval [$5.00000, $5.00000]\n"
	if !strings.Contains(out, want) {
		t.Errorf("report lacks %q:\n%s", want, out)
	}
}

func TestReport_NotReadLinesInTheBody(t *testing.T) {
	f := Facts{Now: day(20), NotRead: []string{"issues: gh: boom", "pull requests: gh: bang"}}

	out := string(Report(f))

	want := "trellis report\nnot read: issues: gh: boom\nnot read: pull requests: gh: bang\ntrees merged: 0"
	if !strings.Contains(out, want) {
		t.Errorf("report lacks %q:\n%s", want, out)
	}
	if strings.Contains(string(Report(Facts{Now: day(20)})), "not read") {
		t.Errorf("a complete report names something not read")
	}
}

func TestReport_SkippedLinesAndFilesAreSaid(t *testing.T) {
	rec := `{"type":"assistant","timestamp":"2026-10-01T09:00:05Z","requestId":"a","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	huge := `{"type":"user","message":{"content":"` + strings.Repeat("x", 9<<20) + `"}}` + "\n"
	tr := rec + "not json at all\n" + huge + "\n" + `{"type":"user"}` + "\n"
	f := Facts{Now: day(20), SkippedFiles: 2, Usage: []Usage{{Tree: "A", Transcript: []byte(tr)}}}

	out := string(Report(f))

	for _, want := range []string{"  transcript lines skipped: 2\n", "  transcript files not read: 2\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if clean := string(Report(Facts{Now: day(20), Usage: []Usage{{Tree: "A", Transcript: []byte(rec)}}})); strings.Contains(clean, "skipped") || strings.Contains(clean, "not read") {
		t.Errorf("a clean transcript names a skip:\n%s", clean)
	}
}

func TestSizeLabel(t *testing.T) {
	for id, want := range map[string]string{
		"claude-opus-5-5":          "large model",
		"claude-opus-9-1[1m]":      "large model",
		"claude-fable-5":           "large model",
		"claude-sonnet-5-20261001": "medium model",
		"claude-haiku-4-5":         "small model",
		"mystery-1":                "other model",
		"<synthetic>":              "other model",
	} {
		if got := SizeLabel(id); got != want {
			t.Errorf("SizeLabel(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestPublished_UnpricedModelIdsBecomeSizes(t *testing.T) {
	line := func(id, model string) string {
		return `{"type":"assistant","timestamp":"2026-10-01T09:00:05Z","requestId":"` + id + `","message":{"model":"` + model + `","usage":{"input_tokens":40,"output_tokens":2}}}` + "\n"
	}
	f := Facts{Now: day(20), Usage: []Usage{{Tree: "A", Transcript: []byte(line("a", "mystery-1") + line("b", "claude-opus-9-1"))}}}

	pub := string(Published(f))
	plain := string(Report(f))

	if want := "  unpriced tokens: 84 (models: large model, other model)\n"; !strings.Contains(pub, want) {
		t.Errorf("published report lacks %q:\n%s", want, pub)
	}
	for _, bad := range []string{"mystery", "claude", "opus"} {
		if strings.Contains(pub, bad) {
			t.Errorf("published report contains %q:\n%s", bad, pub)
		}
	}
	if want := "(models: claude-opus-9-1, mystery-1)"; !strings.Contains(plain, want) {
		t.Errorf("the local report lacks %q:\n%s", want, plain)
	}
}
