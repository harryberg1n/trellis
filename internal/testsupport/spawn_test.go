package testsupport

import (
	"testing"

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
