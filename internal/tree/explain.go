package tree

// What a verb says when it stops: the conflicted files and the next step, the
// failed checks with the lines of their logs that matter. All of it is read
// and filtered here, the same on every platform; no shell tool is involved.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/harryberg1n/trellis/internal/host"
	"github.com/harryberg1n/trellis/internal/objects"
)

// maxFailLines caps the log lines shown for one failed check.
const maxFailLines = 40

// syncError explains a failed rebase. A rebase stopped on conflicts names
// git's CONFLICT lines (git prints them on stdout) and the conflicted files,
// then the next step. The rebase is left where git stopped it.
func syncError(env Env, repo objects.Repo, out string, err error) error {
	files := lines(mustGit(env, repo.Root, "diff", "--name-only", "--diff-filter=U"))
	if len(files) == 0 {
		return fmt.Errorf("tree sync: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "tree sync: conflicts rebasing onto %s", baseOf(repo))
	for _, l := range lines(out) {
		if strings.HasPrefix(l, "CONFLICT") {
			b.WriteString("\n" + l)
		}
	}
	b.WriteString("\nconflicted: " + strings.Join(files, ", "))
	b.WriteString("\nresolve, git add, git rebase --continue; or git rebase --abort")
	return fmt.Errorf("%s", b.String())
}

// mustGit is the output of a git read, "" when it fails.
func mustGit(env Env, dir string, args ...string) string {
	out, err := env.runGit(dir, args...)
	if err != nil {
		return ""
	}
	return out
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// conflictError is the Fixable for a pull request GitHub says conflicts with
// the default branch: the files, found with git merge-tree (git 2.38+), and
// the way out.
func conflictError(env Env, repo objects.Repo) error {
	base := "origin/" + repo.Default
	_, _ = env.runGit(repo.Root, "fetch", "origin") // best effort: the answer is only fresher with it
	where := "(files unknown)"
	if files := conflictFiles(env, repo.Root, base); len(files) > 0 {
		where = "in: " + strings.Join(files, ", ")
	}
	return &Fixable{Msg: fmt.Sprintf("tree merge: conflicts with %s %s\nrun trellis tree sync, resolve, and merge again", base, where)}
}

var objectID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// conflictFiles lists the paths a merge of HEAD into base would conflict on.
// git merge-tree --write-tree exits 1 for a conflicted merge and prints the
// tree id, the conflicted paths (--name-only), a blank line and its messages.
// Anything else, an old git included, gives no files.
func conflictFiles(env Env, dir, base string) []string {
	out, _ := env.runGit(dir, "merge-tree", "--write-tree", "--name-only", base, "HEAD")
	all := strings.Split(strings.ReplaceAll(out, "\r", ""), "\n")
	if len(all) == 0 || !objectID.MatchString(strings.TrimSpace(all[0])) {
		return nil
	}
	var files []string
	for _, l := range all[1:] {
		if strings.TrimSpace(l) == "" {
			break
		}
		files = append(files, l)
	}
	return files
}

var runIDInLink = regexp.MustCompile(`/runs/(\d+)`)

// checksError explains a failed wait: for each failed check its name and
// link and the failing lines of its log. A wait that failed with no failed
// check to show is gh's own output, as a plain error.
func checksError(env Env, gh host.GH, pr int, out string, err error) error {
	checks, _ := gh.Checks(pr)
	var failed []host.Check
	for _, c := range checks {
		if strings.EqualFold(c.Bucket, "fail") {
			failed = append(failed, c)
		}
	}
	if len(failed) == 0 {
		return fmt.Errorf("tree merge: checks of #%d: %w\n%s", pr, err, out)
	}
	noun := "checks"
	if len(failed) == 1 {
		noun = "check"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "tree merge: %d %s failed", len(failed), noun)
	for _, c := range failed {
		fmt.Fprintf(&b, "\n%s  %s", c.Name, c.Link)
		m := runIDInLink.FindStringSubmatch(c.Link)
		if m == nil {
			continue
		}
		log, lerr := gh.FailedLog(m[1])
		if lerr != nil && log == "" {
			env.warn("no log for %s: %v", c.Name, lerr)
			continue
		}
		for _, l := range failingLines(log) {
			b.WriteString("\n  " + l)
		}
	}
	return &Fixable{Msg: b.String()}
}

// logPrefix is what gh run view --log-failed puts before every line: the job
// and step columns and a timestamp.
var logPrefix = regexp.MustCompile(`^(?:[^\t]*\t[^\t]*\t)?\d{4}-\d{2}-\d{2}T[\d:.]+Z ?`)

// failMarks are the substrings that make a log line worth showing.
var failMarks = []string{"--- FAIL", "FAIL\t", "panic:", "Error:", "error:", "##[error]"}

// failingLines are the lines of a failed-step log that name a failure, with
// the job, step and timestamp columns removed, at most maxFailLines.
func failingLines(log string) []string {
	var out []string
	for _, l := range strings.Split(log, "\n") {
		l = logPrefix.ReplaceAllString(strings.TrimRight(l, "\r"), "")
		for _, m := range failMarks {
			if strings.Contains(l, m) {
				out = append(out, l)
				break
			}
		}
		if len(out) == maxFailLines {
			break
		}
	}
	return out
}
