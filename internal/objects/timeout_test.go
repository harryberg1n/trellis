package objects

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/run"
)

// specRunner records the spec it is given and answers with err.
type specRunner struct {
	spec run.Spec
	err  error
}

func (s *specRunner) Light(spec run.Spec) (run.Result, error) {
	s.spec = spec
	return run.Result{}, s.err
}

func TestGitFor_NetworkVerbsGetTwoMinutesAndLocalGitTenMinutes(t *testing.T) {
	for _, c := range []struct {
		args []string
		want time.Duration
	}{
		{[]string{"fetch", "origin"}, 120 * time.Second},
		{[]string{"push", "--force-with-lease", "-u", "origin", "feat"}, 120 * time.Second},
		{[]string{"-c", "core.longpaths=true", "fetch", "origin"}, 120 * time.Second},
		{[]string{"worktree", "add", "-b", "x", "p", "origin/main"}, 10 * time.Minute},
		{[]string{"rebase", "origin/main"}, 10 * time.Minute},
		{[]string{"merge", "--squash", "feat"}, 10 * time.Minute},
		{[]string{"rev-parse", "HEAD"}, 10 * time.Minute},
	} {
		r := &specRunner{}
		if _, err := GitFor(r, "d", nil, c.args...); err != nil {
			t.Fatal(err)
		}
		if r.spec.Timeout != c.want {
			t.Errorf("git %v timeout = %v, want %v", c.args, r.spec.Timeout, c.want)
		}
	}
}

func TestGit_StaysAtThirtySecondsForTheHooks(t *testing.T) {
	r := &specRunner{}
	if _, err := Git(r, "d", nil, "status"); err != nil {
		t.Fatal(err)
	}
	if got, want := r.spec.Timeout, 30*time.Second; got != want {
		t.Errorf("hook git timeout = %v, want %v", got, want)
	}
}

func TestGitFor_ATimeoutSaysTheCommandAndTheDeadline(t *testing.T) {
	r := &specRunner{err: errors.Join(run.ErrTimeout, errors.New("signal: killed"))}

	_, err := GitFor(r, "d", nil, "push", "origin", "feat")

	if err == nil || !strings.Contains(err.Error(), "git push: timed out after 120s") {
		t.Errorf("err = %v, want it to say git push: timed out after 120s", err)
	}
	if !errors.Is(err, run.ErrTimeout) {
		t.Errorf("err = %v, want it to wrap run.ErrTimeout", err)
	}
}
