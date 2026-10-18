package testsupport

import (
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestCountGitSpawns_CountsEachGitStart(t *testing.T) {
	repo := testiso.Repo(t)
	count := CountGitSpawns(t)
	if n := count(); n != 0 {
		t.Fatalf("count before any spawn = %d", n)
	}
	Git(t, repo, "status")
	Git(t, repo, "rev-parse", "HEAD")
	if n := count(); n != 2 {
		t.Errorf("count after two git runs = %d, want 2", n)
	}
}

func TestFakeTool_ExitsWithItsCodeAndSpeaksOnStderr(t *testing.T) {
	FakeTool(t, "trellis-fake", 3, "leaks found: 2")
	res, err := run.Light(clock.Real(), run.Spec{Name: "trellis-fake", Timeout: time.Minute})
	if err == nil || res.ExitCode != 3 || !strings.Contains(string(res.Stderr), "leaks found: 2") {
		t.Errorf("exit %d, stderr %q, err %v; want exit 3 and the stderr text", res.ExitCode, res.Stderr, err)
	}
}
