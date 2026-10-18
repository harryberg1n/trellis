package walls

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/run"
)

// gitleaksTimeout bounds the one scan a commit starts.
const gitleaksTimeout = 30 * time.Second

// notInstalled is the line of a secrets wall that cannot act.
const notInstalled = "not applicable: gitleaks not installed"

var leaksFound = regexp.MustCompile(`leaks found: (\d+)`)

// secrets is the secrets wall (owner decision; gitleaks finding 2026-09): a
// `git commit` while `gitleaks protect --staged` reports a finding. The scan
// is the only process any wall starts for a commit, and a command that is not
// a commit starts none. Without gitleaks the wall is not applicable: the
// action is an allow carrying that line, never a silent pass. A commit that
// stages as it commits (-a) is scanned for what is already staged.
func secrets(a *analysis) (kernel.Action, bool) {
	if a.r == nil {
		return kernel.Action{}, false
	}
	for _, c := range a.gitCalls() {
		if c.Verb != "commit" || slices.Contains(c.Args, "--dry-run") {
			continue
		}
		f, ok := repoAt(c.Dir)
		if !ok {
			continue
		}
		res, err := a.r.Light(run.Spec{
			Name: "gitleaks", Args: []string{"protect", "--staged", "--no-banner", "--redact"},
			Dir: f.Root, Timeout: gitleaksTimeout,
		})
		switch {
		case err == nil:
			continue
		case errors.Is(err, exec.ErrNotFound):
			return kernel.Action{RowID: Secrets, Verdict: kernel.VerdictAllow, Line: notInstalled}, true
		case res.ExitCode == 1:
			cause := "gitleaks reports a finding in the staged changes"
			if m := leaksFound.FindSubmatch(append(res.Stderr, res.Stdout...)); m != nil {
				cause = fmt.Sprintf("gitleaks reports %s finding(s) in the staged changes", m[1])
			}
			return kernel.Action{RowID: Secrets, Verdict: kernel.VerdictDeny, Line: DenyLine(Secrets, cause)}, true
		default:
			return kernel.Action{
				RowID: Secrets, Verdict: kernel.VerdictAllow,
				Line: fmt.Sprintf("not applicable: gitleaks failed (%s)", firstLine(err.Error())),
			}, true
		}
	}
	return kernel.Action{}, false
}
