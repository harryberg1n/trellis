package testsupport

import (
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
