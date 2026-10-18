package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/host"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/measure"
	"github.com/harryberg1n/trellis/internal/objects"
	runpkg "github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/store"
)

// reportRepo is the repository the weekly report issue is filed on. Scores
// leave the box only as aggregates, and only here, never on the consumer
// repository.
const reportRepo = "harryberg1n/trellis"

// historyWindow is how far back the report reads the default branch.
const historyWindow = 60 * 24 * time.Hour

// realReportEnv is the environment of the running process: the working
// directory, the state root for it, real children and the wall clock.
func realReportEnv() (reportEnv, error) {
	wd, err := os.Getwd()
	if err != nil {
		return reportEnv{}, err
	}
	root, err := store.Root(wd)
	if err != nil {
		return reportEnv{}, err
	}
	c := clock.Real()
	return reportEnv{Repo: wd, Root: root, Runner: runpkg.Exec{Clock: c}, Clock: c}, nil
}

// reportEnv is what the report verb touches: the repository, the runner that
// starts git and gh, the clock and the state root.
type reportEnv struct {
	Repo   string // absolute path inside the repository
	Root   string // the state root
	Runner runpkg.Runner
	Clock  clock.Clock
}

// reportCmd runs `trellis report [--issue]`. All facts are gathered here and
// handed to measure.
func reportCmd(args []string, stdout, stderr io.Writer, env reportEnv) int {
	fs := newFlagSet("report", "report [--issue]", stderr)
	issue := fs.Bool("issue", false, "file the aggregate report as an issue on "+reportRepo)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	repo, err := objects.Open(env.Repo)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis report: %v\n", err)
		return 1
	}
	root := repo.Root
	facts, name, err := gatherFacts(repo, env, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis report: %v\n", err)
		return 1
	}
	body := measure.Report(facts)
	if !*issue {
		_, _ = stdout.Write(body)
		return 0
	}
	title := fmt.Sprintf("report %s %s", name, measure.Week(facts.Now))
	gh := host.GH{Runner: env.Runner, Dir: root}
	msg, err := fileReport(gh, title, string(body))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis report: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, msg)
	return 0
}

// fileReport creates the week's issue, or edits it when one with the title
// exists.
func fileReport(gh host.GH, title, body string) (string, error) {
	found, err := gh.ListIssues(reportRepo, title+" in:title")
	if err != nil {
		return "", err
	}
	for _, is := range found {
		if is.Title == title {
			if err := gh.EditIssue(reportRepo, is.Number, body); err != nil {
				return "", err
			}
			return fmt.Sprintf("updated %s#%d", reportRepo, is.Number), nil
		}
	}
	url, err := gh.CreateIssue(reportRepo, title, body)
	if err != nil {
		return "", err
	}
	return "filed " + url, nil
}

// gatherFacts reads the default branch history, the escape issues, the event
// log and the transcripts the log names. name is the repository's name.
func gatherFacts(repo objects.Repo, env reportEnv, stderr io.Writer) (measure.Facts, string, error) {
	now := env.Clock.Now()
	facts := measure.Facts{Now: now}

	root := repo.Root
	name := repoName(repo.Remote, root)

	st, err := storeIn(env.Root, repo, env.Clock)
	if err != nil {
		return facts, name, fmt.Errorf("opening the event log: %w", err)
	}
	facts.Events, err = st.Read(0)
	if err != nil {
		return facts, name, fmt.Errorf("reading the event log: %w", err)
	}

	facts.Merges, err = readHistory(env.Runner, repo, now, facts.Events)
	if err != nil {
		return facts, name, err
	}

	if repo.Remote == "" {
		_, _ = fmt.Fprintln(stderr, "trellis report: no remote: issues and pull requests not read")
	} else {
		readGitHub(&facts, env, root, stderr)
	}

	seen := map[string]bool{}
	for _, ev := range facts.Events {
		p := ev.Detail[kernel.DetailTranscript]
		if ev.Kind != kernel.EvSessionUsage || p == "" || seen[p] {
			continue
		}
		seen[p] = true
		raw, err := os.ReadFile(p)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "trellis report: a transcript was not read: %v\n", err)
			continue
		}
		facts.Usage = append(facts.Usage, measure.Usage{Tree: ev.Tree, Session: ev.Session, Transcript: raw})
	}
	return facts, name, nil
}

// readGitHub fills the escape issues and the merged pull requests of the
// repository's remote; a failed read is reported and leaves its part empty.
func readGitHub(facts *measure.Facts, env reportEnv, root string, stderr io.Writer) {
	issues, err := host.GH{Runner: env.Runner, Dir: root}.ListIssues("", "escape:")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis report: escape issues not read: %v\n", err)
	}
	for _, is := range issues {
		facts.Issues = append(facts.Issues, measure.Issue{Number: is.Number, Title: is.Title, Body: is.Body, At: is.CreatedAt})
	}

	prs, err := host.GH{Runner: env.Runner, Dir: root}.ListMergedPRs("")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis report: merged pull requests not read: %v\n", err)
	}
	for _, p := range prs {
		facts.PRs = append(facts.PRs, measure.PR{Number: p.Number, Branch: p.Branch})
	}
}

// repoName is the last element of the remote URL without ".git", else the
// directory name.
func repoName(remote, root string) string {
	if remote != "" {
		u := strings.TrimSuffix(strings.TrimRight(remote, "/"), ".git")
		if i := strings.LastIndexAny(u, "/:"); i >= 0 {
			return u[i+1:]
		}
		return u
	}
	return filepath.Base(root)
}

const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

var (
	trailingPR = regexp.MustCompile(`\(#(\d+)\)\s*$`)
	mergePR    = regexp.MustCompile(`^Merge pull request #(\d+) from \S+?/(\S+)`)
)

// readHistory is `git log --first-parent` on the default branch within the
// window, each commit with the line ranges it touched.
func readHistory(r runpkg.Runner, repo objects.Repo, now time.Time, events []kernel.Event) ([]measure.Merge, error) {
	root, branch := repo.Root, repo.History
	since := now.Add(-historyWindow).UTC().Format(time.RFC3339)
	out, err := objects.Git(r, root, nil, "log", "--first-parent", "--since="+since,
		"--format=%H"+fieldSep+"%cI"+fieldSep+"%s"+fieldSep+"%b"+recordSep, branch)
	if err != nil {
		return nil, err
	}
	treeOfPR := map[int]string{}
	landed := map[string]kernel.Event{} // tree.merged events by the sha they landed as
	for _, ev := range events {
		if sha := ev.Detail[kernel.DetailMerge]; ev.Kind == kernel.EvTreeMerged && sha != "" {
			landed[strings.ToLower(sha)] = ev
		}
		if n, err := strconv.Atoi(ev.Detail[kernel.DetailPR]); err == nil && ev.Tree != "" {
			treeOfPR[n] = ev.Tree
		}
	}
	var merges []measure.Merge
	for _, rec := range strings.Split(string(out), recordSep) {
		rec = strings.TrimLeft(rec, "\r\n")
		f := strings.SplitN(rec, fieldSep, 4)
		if len(f) != 4 {
			continue
		}
		at, err := time.Parse(time.RFC3339, f[1])
		if err != nil {
			continue
		}
		m := measure.Merge{Sha: f[0], Subject: f[2], Body: f[3], At: at}
		if ev, ok := landed[strings.ToLower(m.Sha)]; ok {
			// a merge the verb recorded: the sha is the link, whatever the subject says
			m.Tree = ev.Tree
			m.PR, _ = strconv.Atoi(ev.Detail[kernel.DetailPR])
		} else if g := trailingPR.FindStringSubmatch(m.Subject); g != nil {
			m.PR, _ = strconv.Atoi(g[1])
		} else if g := mergePR.FindStringSubmatch(m.Subject); g != nil {
			m.PR, _ = strconv.Atoi(g[1])
			m.Tree = path.Clean(g[2])
		}
		if tree, ok := treeOfPR[m.PR]; ok && m.PR > 0 {
			m.Tree = tree
		}
		diff, err := objects.Git(r, root, nil, "show", "-m", "--first-parent", "-U0", "--no-color", "--format=", m.Sha)
		if err != nil {
			return nil, err
		}
		m.Added, m.Removed = measure.ParseHunks(string(diff))
		merges = append(merges, m)
	}
	return merges, nil
}
