package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
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
	facts, id, err := gatherFacts(repo, env, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "trellis report: %v\n", err)
		return 1
	}
	if !*issue {
		_, _ = stdout.Write(measure.Report(facts))
		return 0
	}
	body := measure.Published(facts) // filed on a public repository: model ids are sizes
	title := fmt.Sprintf("report %s %s", id[:8], measure.Week(facts.Now))
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
// exists. It finds the issue by listing every issue, open and closed, and
// matching the title exactly: a search index lags an issue just created, and
// a listing that fails stops the verb before anything is opened.
//
// Adapted from aphrollo-tools internal/report/deliver.go:60-62 at 12b3724e.
// Incident: a search that missed a just-filed issue opened a duplicate.
func fileReport(gh host.GH, title, body string) (string, error) {
	found, err := gh.ListIssues(reportRepo, "")
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
// log and the transcripts the log names. id is the repository's id: the report
// is filed on another repository, so the consumer's name never leaves the box
// and only the first digits of its id name the issue.
func gatherFacts(repo objects.Repo, env reportEnv, stderr io.Writer) (measure.Facts, string, error) {
	now := env.Clock.Now()
	facts := measure.Facts{Now: now}

	root := repo.Root
	id := store.RepoID(repo.Remote, repo.PrimaryRoot())

	st, err := storeIn(env.Root, repo, env.Clock)
	if err != nil {
		return facts, id, fmt.Errorf("opening the event log: %w", err)
	}
	facts.Events, err = st.Read(0)
	if err != nil {
		return facts, id, fmt.Errorf("reading the event log: %w", err)
	}

	facts.Merges, err = readHistory(env.Runner, repo, now, facts.Events)
	if err != nil {
		return facts, id, err
	}

	if repo.Remote == "" {
		noteNotRead(&facts, stderr, "no remote: issues and pull requests not read", "issues and pull requests: no remote")
	} else {
		readGitHub(&facts, env, root, stderr)
	}

	readUsage(&facts, stderr)
	return facts, id, nil
}

// session is what the log says of one session's transcripts: the paths it
// recorded and the tree it was on at each turn's end.
type session struct {
	id    string
	paths []string
	marks []measure.TreeMark
	last  string
}

// readUsage reads the transcripts the log names. A reply is charged to the
// tree the session was on at the Stop that ended its turn, so a session that
// moved between trees is split, and a session killed before its SessionEnd
// still has its Stops. A session with no readable transcript is counted.
func readUsage(facts *measure.Facts, stderr io.Writer) {
	var order []*session
	byID := map[string]*session{}
	for _, ev := range facts.Events {
		if ev.Kind != kernel.EvSessionUsage && ev.Kind != kernel.EvTurnAnchored {
			continue
		}
		s := byID[ev.Session]
		if s == nil {
			s = &session{id: ev.Session}
			byID[ev.Session] = s
			order = append(order, s)
		}
		if p := ev.Detail[kernel.DetailTranscript]; p != "" && !slices.Contains(s.paths, p) {
			s.paths = append(s.paths, p)
		}
		s.marks = append(s.marks, measure.TreeMark{At: ev.At, Tree: ev.Tree})
		s.last = ev.Tree
	}
	for _, s := range order {
		read := 0
		for _, p := range s.paths {
			raw, err := os.ReadFile(p)
			if err != nil {
				// A file that cannot be read is counted in SkippedFiles; the
				// session is counted in MissingTranscripts only when nothing
				// of it was read, its subagents included.
				_, _ = fmt.Fprintf(stderr, "trellis report: a transcript was not read: %v\n", err)
				facts.SkippedFiles++
			} else {
				read++
			}
			// The subagents' replies join the session's, so one requestId is
			// one reply wherever it was written.
			for _, sub := range subagentTranscripts(p) {
				more, err := os.ReadFile(sub)
				if err != nil {
					_, _ = fmt.Fprintf(stderr, "trellis report: a subagent transcript was not read: %v\n", err)
					facts.SkippedFiles++
					continue
				}
				raw = append(append(raw, '\n'), more...)
				read++
			}
			if len(raw) == 0 {
				continue
			}
			facts.Usage = append(facts.Usage, measure.Usage{Tree: s.last, Session: s.id, Transcript: raw, Marks: s.marks})
		}
		if read == 0 {
			facts.MissingTranscripts++
		}
	}
}

// readGitHub fills the escape issues and the merged pull requests of the
// repository's remote; a failed read is reported and leaves its part empty.
func readGitHub(facts *measure.Facts, env reportEnv, root string, stderr io.Writer) {
	issues, err := host.GH{Runner: env.Runner, Dir: root}.ListIssues("", "escape:")
	if err != nil {
		noteNotRead(facts, stderr, fmt.Sprintf("escape issues not read: %v", err), "escape issues")
	}
	for _, is := range issues {
		facts.Issues = append(facts.Issues, measure.Issue{Number: is.Number, Title: is.Title, Body: is.Body, At: is.CreatedAt})
	}

	prs, err := host.GH{Runner: env.Runner, Dir: root}.ListMergedPRs("")
	if err != nil {
		noteNotRead(facts, stderr, fmt.Sprintf("merged pull requests not read: %v", err), "merged pull requests")
	}
	for _, p := range prs {
		facts.PRs = append(facts.PRs, measure.PR{Number: p.Number, Branch: p.Branch})
	}
}

// fieldSep separates the fields of a commit header. A NUL brackets each header
// in the log output: git never prints one in a text diff or a message, so no
// content can split a record.
const (
	fieldSep = "\x1f"
	nul      = "\x00"
)

// historyTimeout is the time the one history call gets: five seconds a day of
// the window, where each commit once had its own thirty.
const historyTimeout = historyWindow / (24 * time.Hour) * 5 * time.Second

var (
	trailingPR = regexp.MustCompile(`\(#(\d+)\)\s*$`)
	mergePR    = regexp.MustCompile(`^Merge pull request #(\d+) from \S+?/(\S+)`)
)

// readHistory is `git log --first-parent` on the default branch within the
// window, each commit with the line ranges it touched: one git call, the
// header of each commit between a record mark and a header end, its diff
// against the first parent after.
func readHistory(r runpkg.Runner, repo objects.Repo, now time.Time, events []kernel.Event) ([]measure.Merge, error) {
	root, branch := repo.Root, repo.History
	since := now.Add(-historyWindow).UTC().Format(time.RFC3339)
	res, err := r.Light(runpkg.Spec{Name: "git", Dir: root, Timeout: historyTimeout, Args: []string{
		"log", "--first-parent", "-m", "-p", "-U0", "--no-color", "--since=" + since,
		"--format=%x00%H" + fieldSep + "%cI" + fieldSep + "%s" + fieldSep + "%b%x00", branch,
	}})
	if err != nil {
		return nil, fmt.Errorf("reading the history: %w", err)
	}
	out := res.Stdout
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
	parts := strings.Split(string(out), nul) // "", header, diff, header, diff, ...
	for i := 1; i+1 < len(parts); i += 2 {
		f := strings.SplitN(parts[i], fieldSep, 4)
		diff := parts[i+1]
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
		m.Added, m.Removed = measure.ParseHunks(diff)
		merges = append(merges, m)
	}
	return merges, nil
}

// noteNotRead says on stderr, and in the report body, a fact the report could
// not gather: the body is what a filed issue carries, so it must say what it
// lacks.
//
// Adapted from aphrollo-tools internal/report/speed.go:69-74,278-290 at
// 12b3724e. Incident: a partial report read as a complete one.
func noteNotRead(facts *measure.Facts, stderr io.Writer, onStderr, inBody string) {
	_, _ = fmt.Fprintf(stderr, "trellis report: %s\n", onStderr)
	facts.NotRead = append(facts.NotRead, inBody)
}

// subagentTranscripts are the transcripts of the subagents a session ran: the
// files <session>/subagents/*.jsonl beside the session file the hook recorded.
// A session with none has no such directory.
//
// Adapted from aphrollo-tools internal/report/usage_scan.go:164-192 at
// 12b3724e. Incident: a tree built by a coordinator with builders was charged
// the coordinator's tokens only.
func subagentTranscripts(session string) []string {
	dir := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries { // sorted by name: the same bytes every run
		if strings.HasSuffix(e.Name(), ".jsonl") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}
