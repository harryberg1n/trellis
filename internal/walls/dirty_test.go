package walls

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
)

func TestDirty_DescribesWhatNoCommitHolds(t *testing.T) {
	repo := testiso.Repo(t)
	r := run.Exec{Clock: clock.Real()}
	if got, err := Dirty(r, repo); err != nil || got != "" {
		t.Fatalf("clean repo: Dirty = %q, %v; want empty", got, err)
	}

	testiso.Write(t, filepath.Join(repo, "README.md"), "changed\n")
	testiso.Write(t, filepath.Join(repo, "new.txt"), "x")

	got, err := Dirty(r, repo)
	want := "1 unstaged file, 1 untracked file"
	if err != nil || got != want {
		t.Errorf("Dirty = %q, %v; want %q", got, err, want)
	}
}

func TestDirty_IgnoreUntrackedCountsOnlyWhatGitTracks(t *testing.T) {
	repo := testiso.Repo(t)
	r := run.Exec{Clock: clock.Real()}
	testiso.Write(t, filepath.Join(repo, "new.txt"), "x")
	if got, err := Dirty(r, repo, IgnoreUntracked()); err != nil || got != "" {
		t.Fatalf("untracked only: Dirty = %q, %v; want empty", got, err)
	}

	testiso.Write(t, filepath.Join(repo, "README.md"), "changed\n")

	got, err := Dirty(r, repo, IgnoreUntracked())
	if err != nil || got != "1 unstaged file" {
		t.Errorf("Dirty = %q, %v; want %q", got, err, "1 unstaged file")
	}
}

func TestDirty_StatusTakesNoOptionalLocks(t *testing.T) {
	repo := testiso.Repo(t)
	r := &run.Recorder{Inner: run.Exec{Clock: clock.Real()}}
	if _, err := Dirty(r, repo); err != nil {
		t.Fatal(err)
	}
	specs := r.Specs()
	if len(specs) != 1 || !slices.Contains(specs[0].Args, "--no-optional-locks") {
		t.Errorf("specs %+v, want one status with --no-optional-locks", specs)
	}
}
