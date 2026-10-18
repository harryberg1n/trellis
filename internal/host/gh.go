// Package host talks to the code host. In M0 it is a minimal gh light runner
// for issue list, create and edit; the full Host port is M1.
package host

import (
	"encoding/json"
	"fmt"
	"strconv"
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
	res, err := g.Runner.Light(run.Spec{Name: "gh", Args: args, Dir: g.Dir, Timeout: ghTimeout, Stdin: stdin})
	if err != nil {
		return nil, fmt.Errorf("host: gh %s: %w", args[0]+" "+args[1], err)
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
