package testsupport

import (
	"path/filepath"
	"testing"

	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestRecorder_CountsWhatItRuns(t *testing.T) {
	testiso.FakeTool(t, "trellis-fake", 0, "")
	rec := Recorder()
	if _, err := rec.Light(run.Spec{Name: "trellis-fake", Timeout: 60e9}); err != nil {
		t.Fatal(err)
	}
	if rec.Count() != 1 || rec.Kinds()[0] != "trellis-fake" {
		t.Errorf("count %d kinds %v, want 1 [trellis-fake]", rec.Count(), rec.Kinds())
	}
}

func TestFakeRunner_AnswersItsProgramAndPassesTheRestOn(t *testing.T) {
	testiso.FakeTool(t, "trellis-other", 0, "")
	inner := Recorder()
	f := &FakeRunner{Name: "gh", Inner: inner, Answer: Stdout("[]")}

	res, err := f.Light(run.Spec{Name: "gh", Args: []string{"issue", "list"}})
	if err != nil || string(res.Stdout) != "[]" {
		t.Fatalf("gh answered %q, %v; want []", res.Stdout, err)
	}
	if _, err := f.Light(run.Spec{Name: "trellis-other", Timeout: 60e9}); err != nil {
		t.Fatal(err)
	}
	if got := f.Specs(); len(got) != 1 || got[0].Args[0] != "issue" {
		t.Errorf("fake answered %+v, want only the gh spec", got)
	}
	if inner.Count() != 1 {
		t.Errorf("inner ran %d specs, want the one that was not gh", inner.Count())
	}
}

func TestPendingEvents_ReadsParkedFilesInNameOrder(t *testing.T) {
	dir := t.TempDir()
	testiso.Write(t, filepath.Join(dir, "events.pending", "b.json"), `{"kind":"edit","lane":"b"}`)
	testiso.Write(t, filepath.Join(dir, "events.pending", "a.json"), `{"kind":"stop","lane":"a"}`)
	got := PendingEvents(t, dir)
	if len(got) != 2 || got[0].Lane != "a" || got[1].Kind != "edit" {
		t.Errorf("events %+v, want a then b", got)
	}
	if got := PendingEvents(t, t.TempDir()); len(got) != 0 {
		t.Errorf("events %+v from an empty directory, want none", got)
	}
}
