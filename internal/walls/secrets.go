package walls

// The report parsing, the three ways to clear a false positive and the choice
// of the staged form by version are copied from aphrollo-tools
// internal/tdd/precommit/secretscan.go at 12b3724e (secretScanClearing,
// secretScanParse, secretScanArgs, gitleaksVersion). Incident #1255: a
// secret-looking line passed every local stage and failed CI's scan, and only a
// rewrite of the lane's history cleared it; the refusal now names the file, the
// line, the rule and the fingerprint, and how to clear a false positive. What
// changed: the finding exit code is 2 (gitleaks' own failures exit 1), the
// version is asked through the runner and kept in the state root, and the
// refusal is the trellis deny line.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/store"
)

// gitleaksTimeout bounds each scan a commit starts: the row's budget.
const gitleaksTimeout = 10 * time.Second

// notInstalled is the line of a secrets wall that cannot act.
const notInstalled = "not applicable: gitleaks not installed"

// findingExit is the exit code gitleaks is told to use for a finding. Its
// default, 1, is also what it exits with on a fatal error, so only this
// distinct code is a finding.
const findingExit = "2"

// scanTail is what every scan carries: no banner, secrets redacted, the
// findings as a JSON report on standard output.
var scanTail = []string{"--no-banner", "--redact", "--exit-code", findingExit, "--report-format", "json", "--report-path", "-"}

// scanArgs is the scan of the index (staged) or of the uncommitted changes of
// the working tree. gitleaks 8.19 moved both to `git --staged` and `git
// --pre-commit`; older ones have `protect`, which still ships. A version that
// was not named is taken as current.
func scanArgs(version string, staged bool) []string {
	legacy := legacyGitleaks(version)
	switch {
	case legacy && staged:
		return slices.Concat([]string{"protect", "--staged"}, scanTail)
	case legacy:
		return slices.Concat([]string{"protect"}, scanTail)
	case staged:
		return slices.Concat([]string{"git", "--staged"}, scanTail)
	}
	return slices.Concat([]string{"git", "--pre-commit"}, scanTail)
}

// legacyGitleaks reports whether version names a gitleaks before 8.19.
func legacyGitleaks(version string) bool {
	var major, minor, patch int
	n, _ := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch)
	return n == 3 && (major < 8 || major == 8 && minor < 19)
}

var (
	leaksFound = regexp.MustCompile(`leaks found: (\d+)`)
	semver     = regexp.MustCompile(`\d+\.\d+\.\d+`)
)

// clearing is the three ways a false positive is cleared, in the words the
// refusal prints.
const clearing = `to clear a false positive, any one of:
    1. put gitleaks:allow in a comment on that line, in the file's own comment syntax (// gitleaks:allow, # gitleaks:allow, <!-- gitleaks:allow -->)
    2. add the finding's fingerprint, printed above, to .gitleaksignore (one per line)
    3. add an [allowlist] to .gitleaks.toml
  otherwise remove the secret`

// finding is one line the scanner flagged.
type finding struct {
	File string
	Line int
	Rule string
	// Fingerprint is what .gitleaksignore takes to clear this one finding.
	Fingerprint string
}

// parseReport reads gitleaks' JSON report.
func parseReport(out []byte) ([]finding, error) {
	var raw []struct {
		RuleID      string
		StartLine   int
		File        string
		Fingerprint string
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("unreadable gitleaks report: %w", err)
	}
	findings := make([]finding, 0, len(raw))
	for _, r := range raw {
		findings = append(findings, finding{File: filepath.ToSlash(r.File), Line: r.StartLine, Rule: r.RuleID, Fingerprint: r.Fingerprint})
	}
	return findings, nil
}

// findingsCause is the deny cause for a report: each finding by file, line,
// rule and fingerprint, then the three ways to clear a false positive. A scan
// whose report cannot be read is still a finding (the exit code says so) and is
// said without the list.
func findingsCause(res run.Result) string {
	found, err := parseReport(res.Stdout)
	if err != nil || len(found) == 0 {
		if m := leaksFound.FindSubmatch(append(res.Stderr, res.Stdout...)); m != nil {
			return fmt.Sprintf("gitleaks reports %s finding(s) in the changes being committed", m[1])
		}
		return "gitleaks reports a finding in the changes being committed"
	}
	lines := make([]string, 0, len(found))
	for _, f := range found {
		line := fmt.Sprintf("%s:%d  %s", f.File, f.Line, f.Rule)
		if f.Fingerprint != "" {
			line += "  fingerprint " + f.Fingerprint
		}
		lines = append(lines, line)
	}
	return fmt.Sprintf("gitleaks reports %d finding(s) in the changes being committed:\n  %s\n%s", len(found), strings.Join(lines, "\n  "), clearing)
}

// secrets is the secrets wall (owner decision; gitleaks finding 2026-09): a
// `git commit` while gitleaks reports a finding in the staged changes. Only a
// commit starts a process (two on the first commit after gitleaks changed: the
// version, then the scan). A commit that stages as it commits (-a, a path,
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
		bin, err := exec.LookPath("gitleaks")
		if err != nil {
			return kernel.Action{RowID: Secrets, Verdict: kernel.VerdictAllow, Line: notInstalled}, true
		}
		version := gitleaksVersion(a.r, bin, f.Root)
		scans := [][]string{scanArgs(version, true)}
		if added || stagesWhileCommitting(c.Args) {
			scans = append(scans, scanArgs(version, false))
		}
		for _, args := range scans {
			if act, done := scan(a, f.Root, args); done {
				return act, true
			}
		}
	}
	return kernel.Action{}, false
}

// gitleaksVersion is the version the gitleaks at bin reports, asked once per
// binary: the answer is kept in the state root under the binary's path and
// mtime, so a reinstall asks again and a cached hook starts no second process.
// "" when it named none (the current form is then used).
func gitleaksVersion(r run.Runner, bin, dir string) string {
	info, err := os.Stat(bin)
	if err != nil {
		return ""
	}
	root, rootErr := store.Root("")
	if rootErr == nil {
		if v, ok := store.LoadToolVersion(root, bin, info.ModTime()); ok {
			return v
		}
	}
	res, _ := r.Light(run.Spec{Name: "gitleaks", Args: []string{"version"}, Dir: dir, Timeout: gitleaksTimeout})
	version := semver.FindString(string(res.Stdout) + string(res.Stderr))
	if rootErr == nil && version != "" { // a failed ask is asked again next time
		_ = store.SaveToolVersion(root, bin, info.ModTime(), version) // a cache that cannot be written is asked again
	}
	return version
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
		return kernel.Action{RowID: Secrets, Verdict: kernel.VerdictDeny, Line: DenyLine(Secrets, findingsCause(res))}, true
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
