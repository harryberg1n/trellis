package walls_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/harryberg1n/trellis/internal/hook"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/testiso"
	"github.com/harryberg1n/trellis/internal/testsupport"
	"github.com/harryberg1n/trellis/internal/walls"
)

// world is a primary checkout on main and a linked worktree (a lane) that
// holds one modified tracked file and one untracked file. The branch unmerged
// has a commit that no other ref reaches.
type world struct{ primary, lane string }

func newWorld(t testing.TB) world {
	t.Helper()
	primary := testiso.Repo(t)
	lane := filepath.Join(filepath.Dir(primary), "lane")
	testsupport.Git(t, primary, "worktree", "add", "-q", "-b", "lane/x", lane)
	tree := testsupport.Git(t, primary, "rev-parse", "HEAD^{tree}")
	c := testsupport.Git(t, primary, "-c", "user.name=t", "-c", "user.email=t@example.invalid",
		"commit-tree", tree, "-p", "HEAD", "-m", "unmerged work")
	testsupport.Git(t, primary, "branch", "unmerged", c)
	testsupport.Write(t, filepath.Join(lane, "README.md"), "changed\n")
	testsupport.Write(t, filepath.Join(lane, "scratch.txt"), "scratch\n")
	return world{primary: primary, lane: lane}
}

// fill expands the placeholders of a fixture payload.
func (w world) fill(raw string) []byte {
	esc := func(s string) string {
		b, _ := json.Marshal(s)
		return string(b[1 : len(b)-1])
	}
	return []byte(strings.NewReplacer(
		"{{primary}}", esc(w.primary), "{{lane}}", esc(w.lane),
		"{{primary_slash}}", filepath.ToSlash(w.primary), "{{lane_slash}}", filepath.ToSlash(w.lane),
	).Replace(raw))
}

// bash is a PreToolUse payload of a Bash call in dir.
func bash(t testing.TB, dir, command string, session bool) []byte {
	t.Helper()
	m := map[string]any{"cwd": dir, "hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]string{"command": command}}
	if session {
		m["session_id"] = "s-1"
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// decide parses a PreToolUse payload and evaluates the shipped rows on it.
func decide(t testing.TB, payload []byte, r run.Runner) []kernel.Action {
	t.Helper()
	ev, ctx, err := hook.Parse("PreToolUse", payload)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, payload)
	}
	return walls.Decide(walls.Rows(), ev, ctx, r)
}

func verdicts(actions []kernel.Action, v kernel.Verdict) []kernel.Action {
	var out []kernel.Action
	for _, a := range actions {
		if a.Verdict == v {
			out = append(out, a)
		}
	}
	return out
}

func quiet(t testing.TB) run.Runner {
	t.Helper()
	testsupport.FakeTool(t, "gitleaks", 0, "")
	return testsupport.Recorder()
}

func TestWalls_EachIncidentFixtureIsDenied(t *testing.T) {
	w := newWorld(t)
	files, err := filepath.Glob(filepath.Join("testdata", "incidents", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("incident fixtures: %v, %v", files, err)
	}
	seen := map[string]int{}
	for _, f := range files {
		row, _, ok := strings.Cut(strings.TrimSuffix(filepath.Base(f), ".json"), "__")
		if !ok {
			t.Fatalf("%s: a fixture is named <row-id>__<n>.json", f)
		}
		seen[row]++
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			leak := 0
			if row == "secrets" {
				leak = 2
			}
			testsupport.FakeTool(t, "gitleaks", leak, "leaks found: 1")
			actions := decide(t, w.fill(string(raw)), testsupport.Recorder())
			denies := verdicts(actions, kernel.VerdictDeny)
			if len(denies) != 1 || denies[0].RowID != row {
				t.Fatalf("denies %+v, want exactly one, from %s (all actions %+v)", denies, row, actions)
			}
			line := denies[0].Line
			if !strings.HasPrefix(line, "trellis: "+row+": ") || !strings.HasSuffix(line, ".") || !strings.Contains(line, ". Override: ") {
				t.Errorf("deny line %q, want \"trellis: %s: <cause>. Override: <command>.\"", line, row)
			}
		})
	}
	for _, row := range []string{"primary-write", "main-commit", "hook-bypass", "discard-uncommitted", "secrets", "undercover"} {
		if seen[row] == 0 {
			t.Errorf("no incident fixture for %s", row)
		}
	}
}

func TestWalls_DenyLinesNameTheirOverride(t *testing.T) {
	cases := []struct{ id, want string }{
		{"primary-write", "trellis: primary-write: because. Override: trellis allow primary-write."},
		{"discard-uncommitted", "trellis: discard-uncommitted: because. Override: trellis allow discard-uncommitted."},
		{"secrets", "trellis: secrets: because. Override: none."},
		{"undercover", "trellis: undercover: because. Override: none."},
	}
	for _, c := range cases {
		if got := walls.DenyLine(c.id, "because"); got != c.want {
			t.Errorf("DenyLine(%s) = %q, want %q", c.id, got, c.want)
		}
	}
}

func TestWalls_TokenInsideAStringNeverBlocks(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	for _, cmd := range []string{
		`echo "git reset --hard"`,
		`git commit -m "explain --no-verify"`,
		`echo 'git clean -fd' > /dev/null`,
		`echo ok # git checkout -- README.md`,
		`grep -rn "git stash drop" .`,
		`git commit -m "document git -c core.hooksPath=x and git push origin main"`,
		`echo "git branch -D unmerged" "git worktree remove --force x"`,
		`cat <<'EOF'` + "\n" + `git reset --hard` + "\n" + `EOF`,
	} {
		got := decide(t, bash(t, w.lane, cmd, true), r)
		if len(got) != 0 {
			t.Errorf("%q: actions %+v, want none: the token is inside a string, a comment or data", cmd, got)
		}
	}
	ps := []byte(`{"session_id":"s","cwd":` + quote(w.lane) + `,"hook_event_name":"PreToolUse","tool_name":"PowerShell","tool_input":{"command":"Write-Host 'git reset --hard'"}}`)
	if got := decide(t, ps, r); len(got) != 0 {
		t.Errorf("PowerShell string: actions %+v, want none", got)
	}
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// dq is s as a double-quoted shell word: newlines stay newlines, as a commit
// message typed into a Bash call has them.
func dq(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

func TestWalls_RealCommandsStillBlock(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	for _, c := range []struct{ cmd, row string }{
		{`bash -c "git reset --hard"`, "discard-uncommitted"},
		{`echo $(git reset --hard)`, "discard-uncommitted"},
		{`cd ` + filepath.ToSlash(w.lane) + ` && git checkout -- README.md`, "discard-uncommitted"},
		{`git -C ` + filepath.ToSlash(w.lane) + ` clean -fd`, "discard-uncommitted"},
		{`git commit -nm "x"`, "hook-bypass"},
		{`git commit -m x --no-verify`, "hook-bypass"},
		{`git push --no-verify`, "hook-bypass"},
		{`git -c core.hooksPath=/tmp/h push`, "hook-bypass"},
		{`GIT_AUTHOR_NAME=x git commit --no-verify -m x`, "hook-bypass"},
		{`echo hi | tee ` + filepath.ToSlash(w.primary) + `/f.txt`, "primary-write"},
		{`sed -i s/a/b/ ` + filepath.ToSlash(w.primary) + `/README.md`, "primary-write"},
		{`cp x ` + filepath.ToSlash(w.primary) + `/y`, "primary-write"},
	} {
		denies := verdicts(decide(t, bash(t, w.lane, c.cmd, true), r), kernel.VerdictDeny)
		if len(denies) != 1 || denies[0].RowID != c.row {
			t.Errorf("%q: denies %+v, want exactly one from %s", c.cmd, denies, c.row)
		}
	}
}

func TestWalls_OrdinaryWorkIsAllowed(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	for _, c := range []struct{ dir, cmd string }{
		{w.lane, `git commit -m "fix the parser"`},
		{w.lane, `git push origin HEAD`},
		{w.lane, `git status`},
		{w.lane, `git checkout main`},
		{w.lane, `git restore --staged README.md`},
		{w.lane, `git reset --soft HEAD~1`},
		{w.lane, `git clean -n`},
		{w.lane, `git stash push -m wip`},
		{w.lane, `git branch -d lane/y`},
		{w.lane, `echo hi > out.txt`},
		{w.lane, `echo hi > /dev/null`},
		{w.lane, `cp README.md copy.md`},
		{w.primary, `git status`},
		{w.primary, `git commit --dry-run`},
		{w.primary, `git merge --abort`},
		{w.primary, `ls -la > /dev/null`},
		{w.primary, `git worktree remove ` + filepath.ToSlash(w.lane)},
		{w.primary, `git branch -D lane/gone`},
	} {
		if got := decide(t, bash(t, c.dir, c.cmd, true), r); len(got) != 0 {
			t.Errorf("%q in %s: actions %+v, want none", c.cmd, filepath.Base(c.dir), got)
		}
	}
}

func TestPrimaryWrite_OnlyWhileThePrimaryIsOnTheDefaultBranch(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	edit := func(cwd, file string) []byte {
		return []byte(`{"session_id":"s","cwd":` + quote(cwd) + `,"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":` + quote(file) + `}}`)
	}
	file := filepath.Join(w.primary, "docs", "a.md")
	if d := verdicts(decide(t, edit(w.primary, file), r), kernel.VerdictDeny); len(d) != 1 || d[0].RowID != "primary-write" {
		t.Fatalf("Edit in the primary on main: denies %+v, want primary-write", d)
	}
	if got := decide(t, edit(w.lane, filepath.Join(w.lane, "docs", "a.md")), r); len(got) != 0 {
		t.Errorf("Edit in a lane: actions %+v, want none", got)
	}
	if got := decide(t, edit(w.lane, filepath.Join(filepath.Dir(w.primary), "outside.md")), r); len(got) != 0 {
		t.Errorf("Edit outside every repository: actions %+v, want none", got)
	}
	// The primary on another branch is not guarded; origin/HEAD names the default.
	testsupport.Git(t, w.primary, "checkout", "-q", "-b", "scratch")
	if got := decide(t, edit(w.primary, file), r); len(got) != 0 {
		t.Errorf("Edit in the primary on scratch: actions %+v, want none", got)
	}
	testsupport.Git(t, w.primary, "update-ref", "refs/remotes/origin/scratch", "HEAD")
	testsupport.Git(t, w.primary, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/scratch")
	if d := verdicts(decide(t, edit(w.primary, file), r), kernel.VerdictDeny); len(d) != 1 {
		t.Errorf("Edit in the primary on the default branch origin/HEAD names: denies %+v, want one", d)
	}
}

func TestSecrets_NotApplicableWithoutGitleaks(t *testing.T) {
	w := newWorld(t)
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(gitPath))
	rec := testsupport.Recorder()
	got := decide(t, bash(t, w.lane, `git commit -m "fix"`, true), rec)
	want := []kernel.Action{{RowID: "secrets", Verdict: kernel.VerdictAllow, Line: "not applicable: gitleaks not installed"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("actions %+v, want %+v", got, want)
	}
}

func TestSecrets_SpawnsOnlyForACommit(t *testing.T) {
	w := newWorld(t)
	testsupport.FakeTool(t, "gitleaks", 0, "")
	rec := testsupport.Recorder()
	for _, cmd := range []string{"git status", "git push origin HEAD", "echo git commit", "ls"} {
		decide(t, bash(t, w.lane, cmd, true), rec)
	}
	if rec.Count() != 0 {
		t.Fatalf("spawns %v for non-commit commands, want none", rec.Kinds())
	}
	decide(t, bash(t, w.lane, `git commit -m "x"`, true), rec)
	spec := rec.Specs()
	if len(spec) != 1 || spec[0].Name != "gitleaks" || strings.Join(spec[0].Args, " ") != "protect --staged --no-banner --redact --exit-code 2" {
		t.Errorf("spawns %+v, want one gitleaks protect --staged --no-banner --redact", spec)
	}
}

func TestDiscard_WorktreeRemoveForceWithUnstagedIsDenied(t *testing.T) {
	w := newWorld(t)
	// A lane with exactly one unstaged file, and a clean one.
	dirty := filepath.Join(filepath.Dir(w.primary), "dirty")
	clean := filepath.Join(filepath.Dir(w.primary), "clean")
	testsupport.Git(t, w.primary, "worktree", "add", "-q", "-b", "dirty/x", dirty)
	testsupport.Git(t, w.primary, "worktree", "add", "-q", "-b", "clean/x", clean)
	testsupport.Write(t, filepath.Join(dirty, "README.md"), "edited\n")
	r := quiet(t)

	got := decide(t, bash(t, w.primary, "git worktree remove --force "+filepath.ToSlash(dirty), true), r)
	denies := verdicts(got, kernel.VerdictDeny)
	if len(denies) != 1 || denies[0].RowID != "discard-uncommitted" || !strings.Contains(denies[0].Line, "1 unstaged file") {
		t.Fatalf("dirty lane: actions %+v, want a discard-uncommitted deny naming \"1 unstaged file\"", got)
	}
	if got := decide(t, bash(t, w.primary, "git worktree remove --force "+filepath.ToSlash(clean), true), r); len(got) != 0 {
		t.Errorf("clean lane: actions %+v, want none", got)
	}
}

func TestDiscard_SpawnsOnlyForDiscardForms(t *testing.T) {
	w := newWorld(t)
	count := testsupport.CountGitSpawns(t)
	rec := quiet(t).(*run.Recorder)
	templates := []string{
		"ls -la", "git status", "git log --oneline -5", "git diff HEAD", "git checkout main", "git checkout -b topic",
		"git restore --staged README.md", "git reset --soft HEAD~1", "git reset HEAD README.md", "git clean -n", "git clean -nd",
		"git stash push -m x", "git stash pop", "git branch -d old", "git branch --list", "git worktree list", "git worktree add ../x -b y",
		"git worktree remove ../x", `echo "git reset --hard"`, `echo "git clean -fd" > /dev/null`, `git commit -m "git checkout -- ."`,
		"go test ./...", "cat README.md | head -5", "cd .. && ls", "grep -rn stash .",
	}
	n := 0
	for i := 0; n < 200; i++ {
		cmd := templates[i%len(templates)]
		if strings.Contains(cmd, "git commit") {
			continue // a commit starts the secrets scan, which is not git
		}
		decide(t, bash(t, w.lane, cmd+strings.Repeat(" ", i/len(templates)), true), rec)
		n++
	}
	if rec.Count() != 0 || count() != 0 {
		t.Fatalf("%d non-discard payloads: %d Light spawns %v, %d git processes; want 0 and 0", n, rec.Count(), rec.Kinds(), count())
	}
	decide(t, bash(t, w.lane, "git reset --hard", true), rec)
	if rec.Count() != 1 || count() != 1 {
		t.Errorf("one discard form: %d Light spawns %v, %d git processes; want exactly 1 (the git status)", rec.Count(), rec.Kinds(), count())
	}
}

func TestDiscard_MeasuresWhatEachFormWouldDestroy(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	cases := []struct{ cmd, want string }{
		{"git reset --hard", "reset --hard discards 1 unstaged file"},
		{"git checkout -- README.md", "checkout -- <paths> discards 1 unstaged file"},
		{"git restore README.md", "restore <paths> discards 1 unstaged file"},
		{"git clean -fd", "clean -fd discards 1 untracked file"},
		{"git stash drop", "stash drop discards a stash entry for good"},
		{"git branch -D unmerged", "branch -D unmerged discards 1 commit unreachable from HEAD or any remote"},
	}
	for _, c := range cases {
		denies := verdicts(decide(t, bash(t, w.lane, c.cmd, true), r), kernel.VerdictDeny)
		if len(denies) != 1 || !strings.Contains(denies[0].Line, ": "+c.want+".") && !strings.Contains(denies[0].Line, ": "+c.want+" ") {
			t.Errorf("%q: denies %+v, want a cause starting %q", c.cmd, denies, c.want)
		}
	}
	// A clean tree costs nothing, and a pushed branch is recoverable.
	clean := filepath.Join(filepath.Dir(w.primary), "clean")
	testsupport.Git(t, w.primary, "worktree", "add", "-q", "-b", "clean/x", clean)
	for _, cmd := range []string{"git reset --hard", "git clean -fd", "git checkout -- README.md"} {
		if got := decide(t, bash(t, clean, cmd, true), r); len(got) != 0 {
			t.Errorf("%q in a clean lane: actions %+v, want none", cmd, got)
		}
	}
	testsupport.Git(t, w.primary, "update-ref", "refs/remotes/origin/unmerged", "unmerged")
	if got := decide(t, bash(t, w.lane, "git branch -D unmerged", true), r); len(got) != 0 {
		t.Errorf("branch -D of a branch a remote has: actions %+v, want none", got)
	}
}

func TestUndercover_TrailerDenied(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	msg := "fix the parser\n\nIt read past the end of the buffer."
	if got := decide(t, bash(t, w.lane, "git commit -m "+dq(msg), true), r); len(got) != 0 {
		t.Fatalf("message without a trailer: actions %+v, want none", got)
	}
	with := msg + "\n\nCo-Authored-By: X <x@y>"
	denies := verdicts(decide(t, bash(t, w.lane, "git commit -m "+dq(with), true), r), kernel.VerdictDeny)
	if len(denies) != 1 || denies[0].RowID != "undercover" || !strings.Contains(denies[0].Line, "Override: none.") {
		t.Fatalf("message with a trailer: denies %+v, want one from undercover with no override", denies)
	}
	for _, cmd := range []string{
		"git commit -am " + dq(with),
		"git commit --message=" + dq(with),
		"git commit -m" + dq(with),
		"git -C " + filepath.ToSlash(w.lane) + " commit -m " + dq("Generated with [Claude Code](https://claude.com/claude-code)"),
		"git commit -m " + dq("ask Sonnet 5.5"),
		"git commit -m \"$(cat <<'EOF'\n" + with + "\nEOF\n)\"",
		"git commit -F - <<'EOF'\n" + with + "\nEOF",
		"gh pr create --title t --body " + dq("body\n\n"+"Claude-Session: https://claude.ai/code/session_1"),
		"gh pr edit 7 --body=" + dq("🤖 Generated with Claude Code"),
	} {
		d := verdicts(decide(t, bash(t, w.lane, cmd, true), r), kernel.VerdictDeny)
		if len(d) != 1 || d[0].RowID != "undercover" {
			t.Errorf("%q: denies %+v, want one from undercover", cmd, d)
		}
	}
	file := filepath.Join(w.lane, "msg.txt")
	testsupport.Write(t, file, with)
	if d := verdicts(decide(t, bash(t, w.lane, "git commit -F msg.txt", true), r), kernel.VerdictDeny); len(d) != 1 || d[0].RowID != "undercover" {
		t.Errorf("-F with a trailer in the file: denies %+v, want one from undercover", d)
	}
	if got := decide(t, bash(t, w.lane, "gh pr create --title t --body "+dq(msg), true), r); len(got) != 0 {
		t.Errorf("clean PR body: actions %+v, want none", got)
	}
}

func TestWallsWarnPeople_NonAgentGetsWarning(t *testing.T) {
	w := newWorld(t)
	testsupport.FakeTool(t, "gitleaks", 2, "leaks found: 1")
	rec := testsupport.Recorder()

	// main-commit, as an agent: a deny. Without a session_id: a warn.
	cmd := "git -C " + filepath.ToSlash(w.primary) + " push origin main"
	agent := decide(t, bash(t, w.lane, cmd, true), rec)
	if d := verdicts(agent, kernel.VerdictDeny); len(d) != 1 || d[0].RowID != "main-commit" {
		t.Fatalf("agent: actions %+v, want a main-commit deny", agent)
	}
	person := decide(t, bash(t, w.lane, cmd, false), rec)
	if wn := verdicts(person, kernel.VerdictWarn); len(wn) != 1 || wn[0].RowID != "main-commit" || len(verdicts(person, kernel.VerdictDeny)) != 0 {
		t.Errorf("person: actions %+v, want one main-commit warn and no deny", person)
	}

	// secrets is a deny for people too.
	commit := decide(t, bash(t, w.lane, `git commit -m "add keys"`, false), rec)
	if d := verdicts(commit, kernel.VerdictDeny); len(d) != 1 || d[0].RowID != "secrets" {
		t.Errorf("person committing a secret: actions %+v, want a secrets deny", commit)
	}
}

func TestRows_AreTheShippedTable(t *testing.T) {
	rows := walls.Rows()
	if len(rows) != len(kernel.Table) || len(rows) == 0 {
		t.Fatalf("Rows() has %d rows, kernel.Table %d", len(rows), len(kernel.Table))
	}
	rows[0].ID = "mutated"
	if kernel.Table[0].ID == "mutated" {
		t.Error("Rows() returns the table's own slice, not a copy")
	}
	if errs := kernel.Validate(walls.Rows()); len(errs) != 0 {
		t.Errorf("Validate(Rows()) = %v", errs)
	}
}

func TestMatch_RowWithNothingToSayDoesNotMatch(t *testing.T) {
	w := newWorld(t)
	ctx := kernel.Context{Tool: "Bash", Args: map[string]string{"cwd": w.lane, "command": "ls"}}
	for _, row := range walls.Rows() {
		if a, ok := walls.Match(row, ctx, testsupport.Recorder()); ok {
			t.Errorf("Match(%s, ls) = %+v, true; want no match", row.ID, a)
		}
	}
}

func TestSecrets_OnlyExitCodeTwoIsAFinding(t *testing.T) {
	w := newWorld(t)
	testsupport.FakeTool(t, "gitleaks", 1, "fatal: not a git repository")
	got := decide(t, bash(t, w.lane, `git commit -m "x"`, true), testsupport.Recorder())
	want := []kernel.Action{{RowID: "secrets", Verdict: kernel.VerdictAllow, Line: "not applicable: gitleaks failed (exit 1)"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("exit 1: actions %+v, want %+v", got, want)
	}
}

func TestSecrets_ScansTheWorkingTreeWhenTheCommitStagesAsItGoes(t *testing.T) {
	w := newWorld(t)
	staged := "protect --staged --no-banner --redact --exit-code 2"
	tree := "protect --no-banner --redact --exit-code 2"
	for _, c := range []struct {
		cmd  string
		want []string
	}{
		{`git commit -m x`, []string{staged}},
		{`git commit -m x -- README.md`, []string{staged, tree}},
		{`git commit -m x README.md`, []string{staged, tree}},
		{`git commit -am x`, []string{staged, tree}},
		{`git commit --all -m x`, []string{staged, tree}},
		{`git commit --amend --no-edit`, []string{staged, tree}},
		{`git add -A && git commit -m x`, []string{staged, tree}},
		{`git add . ; git commit -m x`, []string{staged, tree}},
	} {
		testsupport.FakeTool(t, "gitleaks", 0, "")
		rec := testsupport.Recorder()
		decide(t, bash(t, w.lane, c.cmd, true), rec)
		var got []string
		for _, s := range rec.Specs() {
			if s.Name == "gitleaks" {
				got = append(got, strings.Join(s.Args, " "))
			}
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: gitleaks calls %q, want %q", c.cmd, got, c.want)
		}
	}
}
