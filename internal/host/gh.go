// Package host talks to the code host. In M0 it is a minimal gh light runner
// for issue list, create and edit; the full Host port is M1.
package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/run"
)

// ghTimeout bounds each gh child.
const ghTimeout = 60 * time.Second

// Issue is a GitHub issue as `gh issue list --json` reports it.
type Issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// GH runs gh through Runner in Dir. An empty repo argument leaves the
// repository to gh, which reads it from Dir's remote.
type GH struct {
	Runner run.Runner
	Dir    string
}

func (g GH) gh(stdin []byte, args ...string) ([]byte, error) {
	return g.ghWithin(ghTimeout, stdin, args...)
}

// ghWithin runs gh for at most timeout. On a failure it still returns what gh
// printed on its standard output.
func (g GH) ghWithin(timeout time.Duration, stdin []byte, args ...string) ([]byte, error) {
	res, err := g.Runner.Light(run.Spec{Name: "gh", Args: args, Dir: g.Dir, Timeout: timeout, Stdin: stdin})
	if err != nil {
		return res.Stdout, fmt.Errorf("host: gh %s: %w", strings.Join(args[:min(2, len(args))], " "), err)
	}
	return res.Stdout, nil
}

func repoArgs(repo string) []string {
	if repo == "" {
		return nil
	}
	return []string{"--repo", repo}
}

// ListIssues lists the issues of repo, open and closed, that match search.
func (g GH) ListIssues(repo, search string) ([]Issue, error) {
	args := append([]string{"issue", "list"}, repoArgs(repo)...)
	args = append(args, "--search", search, "--state", "all", "--limit", "500", "--json", "number,title,body,createdAt")
	out, err := g.gh(nil, args...)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("host: reading gh issue list: %w", err)
	}
	return issues, nil
}

// PR is a merged pull request as `gh pr list --json` reports it.
type PR struct {
	Number int    `json:"number"`
	Branch string `json:"headRefName"`
}

// ListMergedPRs lists the merged pull requests of repo.
func (g GH) ListMergedPRs(repo string) ([]PR, error) {
	args := append([]string{"pr", "list"}, repoArgs(repo)...)
	args = append(args, "--state", "merged", "--limit", "500", "--json", "number,headRefName")
	out, err := g.gh(nil, args...)
	if err != nil {
		return nil, err
	}
	var prs []PR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("host: reading gh pr list: %w", err)
	}
	return prs, nil
}

// CreateIssue files an issue and returns the URL gh prints. The body goes in
// on stdin, not in the argument list.
func (g GH) CreateIssue(repo, title, body string) (string, error) {
	args := append([]string{"issue", "create"}, repoArgs(repo)...)
	args = append(args, "--title", title, "--body-file", "-")
	out, err := g.gh([]byte(body), args...)
	return string(trimNL(out)), err
}

// EditIssue replaces the body of issue number.
func (g GH) EditIssue(repo string, number int, body string) error {
	args := append([]string{"issue", "edit", strconv.Itoa(number)}, repoArgs(repo)...)
	args = append(args, "--body-file", "-")
	_, err := g.gh([]byte(body), args...)
	return err
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// watchTimeout bounds `gh pr checks --watch`, which waits for CI.
const watchTimeout = 45 * time.Minute

// PRState is a pull request as `gh pr view --json` reports it.
type PRState struct {
	Number           int    `json:"number"`
	State            string `json:"state"`            // OPEN, CLOSED or MERGED
	Mergeable        string `json:"mergeable"`        // MERGEABLE, CONFLICTING or UNKNOWN
	MergeStateStatus string `json:"mergeStateStatus"` // CLEAN, DIRTY, BEHIND, BLOCKED ...
	HeadSHA          string `json:"headRefOid"`
}

// Check is one check of a pull request as `gh pr check --json` reports it.
type Check struct {
	Name   string `json:"name"`
	Bucket string `json:"bucket"` // pass, fail, pending, skipping or cancel
	Link   string `json:"link"`
}

// ErrNoChecks is what WatchChecks reports for a pull request with no checks.
var ErrNoChecks = errors.New("host: no checks reported")

// Checks lists the checks of pull request number. gh exits non-zero when one
// failed or is pending, and still prints the list, so the list wins.
func (g GH) Checks(number int) ([]Check, error) {
	out, err := g.gh(nil, "pr", "checks", strconv.Itoa(number), "--json", "name,bucket,link")
	var checks []Check
	if jerr := json.Unmarshal(out, &checks); jerr != nil {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("host: reading gh pr checks: %w", jerr)
	}
	return checks, nil
}

// FailedLog is the log of the failed steps of workflow run id.
func (g GH) FailedLog(runID string) (string, error) {
	out, err := g.gh(nil, "run", "view", runID, "--log-failed")
	return string(out), err
}

// ErrNoPR is what PRView reports for a branch with no pull request.
var ErrNoPR = errors.New("host: no pull request for the branch")

// PRView is the pull request of branch, or an error wrapping ErrNoPR.
func (g GH) PRView(branch string) (PRState, error) {
	out, err := g.gh(nil, "pr", "view", branch, "--json", "number,state,mergeable,mergeStateStatus,headRefOid")
	if err != nil {
		if strings.Contains(err.Error(), "no pull requests found") {
			return PRState{}, fmt.Errorf("%w: %s", ErrNoPR, branch)
		}
		return PRState{}, err
	}
	var pr PRState
	if err := json.Unmarshal(out, &pr); err != nil {
		return PRState{}, fmt.Errorf("host: reading gh pr view: %w", err)
	}
	return pr, nil
}

// CreatePR opens a pull request from the current branch of Dir into base,
// titled and described from its commits, and returns the URL gh prints.
func (g GH) CreatePR(base string) (string, error) {
	out, err := g.gh(nil, "pr", "create", "--fill", "--base", base)
	return string(trimNL(out)), err
}

// WatchChecks waits for the checks of pull request number to finish. A failed
// check is an error, and the returned text is what gh printed.
func (g GH) WatchChecks(number int) (string, error) {
	out, err := g.ghWithin(watchTimeout, nil, "pr", "checks", strconv.Itoa(number), "--watch")
	if err != nil && strings.Contains(err.Error(), "no checks reported") {
		err = fmt.Errorf("%w: #%d", ErrNoChecks, number)
	}
	return string(trimNL(out)), err
}

// Ready is nil when gh is installed and logged in.
func (g GH) Ready() error {
	_, err := g.gh(nil, "auth", "status")
	return err
}

// MergePR rebase-merges pull request number and deletes its branch.
func (g GH) MergePR(number int) error {
	_, err := g.gh(nil, "pr", "merge", strconv.Itoa(number), "--rebase", "--delete-branch")
	return err
}
