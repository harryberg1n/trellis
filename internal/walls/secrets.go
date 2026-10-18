package walls

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/run"
)

// gitleaksTimeout bounds each scan a commit starts: the row's budget.
const gitleaksTimeout = 10 * time.Second

// notInstalled is the line of a secrets wall that cannot act.
const notInstalled = "not applicable: gitleaks not installed"

// findingExit is the exit code gitleaks is told to use for a finding. Its
// default, 1, is also what it exits with on a fatal error, so only this
// distinct code is a finding.
const findingExit = "2"

// gitleaksArgs is the pinned flag set. `protect` is the form of gitleaks
// before it moved the pre-commit scan to `git --staged`; it is the one the
// plan names, and it still ships. --staged scans the index; without it
// `protect` scans the uncommitted changes of the working tree.
var (
	stagedArgs = []string{"protect", "--staged", "--no-banner", "--redact", "--exit-code", findingExit}
	treeArgs   = []string{"protect", "--no-banner", "--redact", "--exit-code", findingExit}
)

var leaksFound = regexp.MustCompile(`leaks found: (\d+)`)

// secrets is the secrets wall (owner decision; gitleaks finding 2026-09): a
// `git commit` while `gitleaks protect --staged` reports a finding. Only a
// commit starts a process. A commit that stages as it commits (-a, a path,
// --amend, or a `git add ... && git commit` chain) is scanned twice: the index,
// and the working tree. Without gitleaks the wall is not applicable: the
// action is an allow carrying that line, never a silent pass; so is a gitleaks
// that fails (any exit but 0 and 2) or times out.
func secrets(a *analysis) (kernel.Action, bool) {
	if a.r == nil {
		return kernel.Action{}, false
	}
	added := false
	for _, c := range a.gitCalls() {
		if c.Verb == "add" {
			added = true
			continue
		}
		if c.Verb != "commit" || slices.Contains(c.Args, "--dry-run") {
			continue
		}
		f, ok := repoAt(c.Dir)
		if !ok {
			continue
		}
		scans := [][]string{stagedArgs}
		if added || stagesWhileCommitting(c.Args) {
			scans = append(scans, treeArgs)
		}
		for _, args := range scans {
			if act, done := scan(a, f.Root, args); done {
				return act, true
			}
		}
	}
	return kernel.Action{}, false
}

// scan runs one gitleaks scan. done is false for a clean scan.
func scan(a *analysis, root string, args []string) (kernel.Action, bool) {
	res, err := a.r.Light(run.Spec{Name: "gitleaks", Args: args, Dir: root, Timeout: gitleaksTimeout})
	switch {
	case err == nil:
		return kernel.Action{}, false
	case errors.Is(err, exec.ErrNotFound):
		return kernel.Action{RowID: Secrets, Verdict: kernel.VerdictAllow, Line: notInstalled}, true
	case res.ExitCode == 2 && !errors.Is(err, run.ErrTimeout):
		cause := "gitleaks reports a finding in the changes being committed"
		if m := leaksFound.FindSubmatch(append(res.Stderr, res.Stdout...)); m != nil {
			cause = fmt.Sprintf("gitleaks reports %s finding(s) in the changes being committed", m[1])
		}
		return kernel.Action{RowID: Secrets, Verdict: kernel.VerdictDeny, Line: DenyLine(Secrets, cause)}, true
	}
	why := firstLine(err.Error())
	if errors.Is(err, run.ErrTimeout) {
		why = "timeout"
	} else if res.ExitCode != 0 {
		why = fmt.Sprintf("exit %d", res.ExitCode)
	}
	return kernel.Action{RowID: Secrets, Verdict: kernel.VerdictAllow, Line: fmt.Sprintf("not applicable: gitleaks failed (%s)", why)}, true
}

// stagesWhileCommitting reports whether the commit takes in changes the index
// does not hold yet: -a or --all (also in a short cluster), --amend, or a path
// operand.
func stagesWhileCommitting(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--all" || a == "--amend" || a == "--":
			return true
		case takesValue[a]:
			i++
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if clusterHas(a[1:], 'a') {
				return true
			}
			if clusterTakesValue(a[1:]) && strings.IndexAny(a[1:], "mFCct") == len(a[1:])-1 {
				i++
			}
		default:
			return true // a path
		}
	}
	return false
}
