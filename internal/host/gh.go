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

// ListIssues lists the issues of repo, open and closed, that match search, or
// all of them (up to the limit) when search is empty.
func (g GH) ListIssues(repo, search string) ([]Issue, error) {
	args := append([]string{"issue", "list"}, repoArgs(repo)...)
	if search != "" {
		args = append(args, "--search", search)
	}
	args = append(args, "--state", "all", "--limit", "500", "--json", "number,title,body,createdAt")
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
	ReviewDecision   string `json:"reviewDecision"` // REVIEW_REQUIRED, CHANGES_REQUESTED, APPROVED or ""
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
// failed or is pending, and still prints the list, so the list wins. A pull
// request GitHub has no check runs for yet has an empty list, not an error.
func (g GH) Checks(number int) ([]Check, error) {
	out, err := g.gh(nil, "pr", "checks", strconv.Itoa(number), "--json", "name,bucket,link")
	var checks []Check
	if jerr := json.Unmarshal(out, &checks); jerr != nil {
		if ghSays(err, ghNoChecks) {
			return nil, nil
		}
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

// prFields are the fields PRState reads.
const prFields = "number,state,mergeable,mergeStateStatus,headRefOid,reviewDecision"

// prListLimit bounds how many pull requests of one head PRView reads.
const prListLimit = "30"

// PRView is the pull request of branch, or an error wrapping ErrNoPR. The pull
// request is found by its head with gh pr list, never by `gh pr view <branch>`:
// gh reads a name made of digits as a pull request number. An open pull
// request wins; else the newest.
func (g GH) PRView(branch string) (PRState, error) {
	out, err := g.gh(nil, "pr", "list", "--head", branch, "--state", "all", "--limit", prListLimit, "--json", prFields)
	if err != nil {
		return PRState{}, err
	}
	var prs []PRState
	if err := json.Unmarshal(out, &prs); err != nil {
		return PRState{}, fmt.Errorf("host: reading gh pr list: %w", err)
	}
	for _, pr := range prs {
		if pr.State == "OPEN" {
			return pr, nil
		}
	}
	if len(prs) == 0 {
		return PRState{}, fmt.Errorf("%w: %s", ErrNoPR, branch)
	}
	return prs[0], nil
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
	if ghSays(err, ghNoChecks) {
		err = fmt.Errorf("%w: #%d", ErrNoChecks, number)
	}
	return string(trimNL(out)), err
}

// Ready is nil when gh is installed and logged in.
func (g GH) Ready() error {
	_, err := g.gh(nil, "auth", "status")
	return err
}

// MergeMethod is how gh lands a pull request: its gh flag.
type MergeMethod string

// The merge methods.
const (
	MergeSquash MergeMethod = "--squash"
	MergeRebase MergeMethod = "--rebase"
)

// MergePR merges pull request number by method and deletes its branch.
func (g GH) MergePR(number int, method MergeMethod) error {
	_, err := g.gh(nil, "pr", "merge", strconv.Itoa(number), string(method), "--delete-branch")
	return err
}

// WorkflowCount is the number of workflows the repository gh finds from Dir
// defines. A repository with none never reports a check.
func (g GH) WorkflowCount() (int, error) {
	out, err := g.gh(nil, "api", "repos/{owner}/{repo}/actions/workflows", "--jq", ".total_count")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(string(trimNL(out)))
	if err != nil {
		return 0, fmt.Errorf("host: reading the workflow count: %w", err)
	}
	return n, nil
}

// MergeCommit is the sha of the commit that merged pull request number: the
// squash commit, or the tip of its commits on the base branch for a rebase
// merge. Empty when the pull request is not merged or GitHub has not reported
// the commit yet.
func (g GH) MergeCommit(number int) (string, error) {
	out, err := g.gh(nil, "pr", "view", strconv.Itoa(number), "--json", "mergeCommit")
	if err != nil {
		return "", err
	}
	var pr struct {
		MergeCommit *struct {
			OID string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal(out, &pr); err != nil {
		return "", fmt.Errorf("host: reading gh pr view: %w", err)
	}
	if pr.MergeCommit == nil {
		return "", nil
	}
	return pr.MergeCommit.OID, nil
}

// MergedPR is a merged pull request as `gh pr list --state merged --json`
// reports it: enough to tell whether it carried a given branch tip.
type MergedPR struct {
	Number      int    `json:"number"`
	HeadSHA     string `json:"headRefOid"`
	BaseBranch  string `json:"baseRefName"`
	MergeCommit *struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
}

// MergeSHA is the merge commit's sha, "" when GitHub reports none.
func (p MergedPR) MergeSHA() string {
	if p.MergeCommit == nil {
		return ""
	}
	return p.MergeCommit.OID
}

// MergedPRs lists the merged pull requests that have branch as their head
// branch name. The name alone is not the branch: a name can be reused, and
// gh matches a pull request from any base or fork, so the caller compares the
// commits (HeadSHA, MergeSHA) with the branch tip.
func (g GH) MergedPRs(branch string) ([]MergedPR, error) {
	out, err := g.gh(nil, "pr", "list", "--head", branch, "--state", "merged", "--limit", "30",
		"--json", "number,headRefOid,baseRefName,mergeCommit")
	if err != nil {
		return nil, err
	}
	var prs []MergedPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("host: reading gh pr list: %w", err)
	}
	return prs, nil
}
