package walls_test

import (
	"fmt"
	"os"
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

// world is a primary checkout on main and a linked worktree (a tree) that
// holds one modified tracked file and one untracked file. The branch unmerged
// has a commit that no other ref reaches.
type world struct{ primary, tree string }

func newWorld(t testing.TB) world {
	t.Helper()
	primary := testiso.Repo(t)
	tree := testiso.AddTree(t, primary, "tree", "tree/x")
	tr := testiso.Git(t, primary, "rev-parse", "HEAD^{tree}")
	c := testiso.Git(t, primary, "commit-tree", tr, "-p", "HEAD", "-m", "unmerged work")
	testiso.Git(t, primary, "branch", "unmerged", c)
	testiso.Write(t, filepath.Join(tree, "README.md"), "changed\n")
	testiso.Write(t, filepath.Join(tree, "scratch.txt"), "scratch\n")
	return world{primary: primary, tree: tree}
}

// fill expands the placeholders of a fixture payload.
func (w world) fill(raw string) []byte {
	return testiso.Fill([]byte(raw), map[string]string{
		"@PRIMARY@": w.primary, "@TREE@": w.tree,
		"@PRIMARY_SLASH@": filepath.ToSlash(w.primary), "@TREE_SLASH@": filepath.ToSlash(w.tree),
	})
}

// bash is a PreToolUse payload of a Bash call in dir.
func bash(t testing.TB, dir, command string, session bool) []byte {
	t.Helper()
	return testiso.HookPayload(t, kernel.HookPreToolUse, kernel.ToolBash, dir, map[string]string{"command": command}, sessionIf(session)...)
}

// sessionIf is the payload option of an agent's call, or none for a person's.
func sessionIf(session bool) []testiso.PayloadOption {
	if !session {
		return []testiso.PayloadOption{testiso.AsPerson()}
	}
	return []testiso.PayloadOption{testiso.WithSession("s-1")}
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
	testiso.FakeTool(t, "gitleaks", 0, "")
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
			testiso.FakeTool(t, "gitleaks", leak, "leaks found: 1")
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
		`echo '$(git reset --hard)'`,
		`cat <<'EOF'` + "\n" + `git reset --hard` + "\n" + `EOF`,
	} {
		got := decide(t, bash(t, w.tree, cmd, true), r)
		if len(got) != 0 {
			t.Errorf("%q: actions %+v, want none: the token is inside a string, a comment or data", cmd, got)
		}
	}
	psPayload := ps(t, w.tree, "Write-Host 'git reset --hard'")
	if got := decide(t, psPayload, r); len(got) != 0 {
		t.Errorf("PowerShell string: actions %+v, want none", got)
	}
	for _, cmd := range []string{"<# git reset --hard #>", "git status <# ; git reset --hard #>", "@\"\ngit reset --hard \"x\"\n\"@"} {
		if got := decide(t, ps(t, w.tree, cmd), r); len(got) != 0 {
			t.Errorf("PowerShell %q: actions %+v, want none", cmd, got)
		}
	}
}

// dq is s as a double-quoted shell word: newlines stay newlines, as a commit
// message typed into a Bash call has them.
func dq(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

func TestWalls_RealCommandsStillBlock(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	for _, c := range []struct{ cmd, row string }{
		{`bash -c "git reset --hard"`, "discard-uncommitted"},
		{`echo $(git reset --hard)`, "discard-uncommitted"},
		{`cd ` + filepath.ToSlash(w.tree) + ` && git checkout -- README.md`, "discard-uncommitted"},
		{`git -C ` + filepath.ToSlash(w.tree) + ` clean -fd`, "discard-uncommitted"},
		{`git --config-env foo=BAR reset --hard`, "discard-uncommitted"},
		{`git --super-prefix sub/ reset --hard`, "discard-uncommitted"},
		{`git commit -nm "x"`, "hook-bypass"},
		{`git commit -m x --no-verify`, "hook-bypass"},
		{`git push --no-verify`, "hook-bypass"},
		{`git -c core.hooksPath=/tmp/h push`, "hook-bypass"},
		{`GIT_AUTHOR_NAME=x git commit --no-verify -m x`, "hook-bypass"},
		{`echo hi | tee ` + filepath.ToSlash(w.primary) + `/f.txt`, "primary-write"},
		{`sed -i s/a/b/ ` + filepath.ToSlash(w.primary) + `/README.md`, "primary-write"},
		{`cp x ` + filepath.ToSlash(w.primary) + `/y`, "primary-write"},
	} {
		denies := verdicts(decide(t, bash(t, w.tree, c.cmd, true), r), kernel.VerdictDeny)
		if len(denies) != 1 || denies[0].RowID != c.row {
			t.Errorf("%q: denies %+v, want exactly one from %s", c.cmd, denies, c.row)
		}
	}
}

func TestWalls_OrdinaryWorkIsAllowed(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	for _, c := range []struct{ dir, cmd string }{
		{w.tree, `git commit -m "fix the parser"`},
		{w.tree, `git push origin HEAD`},
		{w.tree, `git status`},
		{w.tree, `git checkout main`},
		{w.tree, `git restore --staged README.md`},
		{w.tree, `git reset --soft HEAD~1`},
		{w.tree, `git clean -n`},
		{w.tree, `git stash push -m wip`},
		{w.tree, `git branch -d tree/y`},
		{w.tree, `echo hi > out.txt`},
		{w.tree, `echo hi > /dev/null`},
		{w.tree, `cp README.md copy.md`},
		{w.primary, `git status`},
		{w.primary, `git commit --dry-run`},
		{w.primary, `git merge --abort`},
		{w.primary, `ls -la > /dev/null`},
		{w.primary, `git worktree remove ` + filepath.ToSlash(w.tree)},
		{w.primary, `git branch -D tree/gone`},
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
		return testiso.HookPayload(t, kernel.HookPreToolUse, kernel.ToolEdit, cwd, map[string]string{"file_path": file}, testiso.WithSession("s"))
	}
	file := filepath.Join(w.primary, "docs", "a.md")
	if d := verdicts(decide(t, edit(w.primary, file), r), kernel.VerdictDeny); len(d) != 1 || d[0].RowID != "primary-write" {
		t.Fatalf("Edit in the primary on main: denies %+v, want primary-write", d)
	}
	if got := decide(t, edit(w.tree, filepath.Join(w.tree, "docs", "a.md")), r); len(got) != 0 {
		t.Errorf("Edit in a tree: actions %+v, want none", got)
	}
	if got := decide(t, edit(w.tree, filepath.Join(filepath.Dir(w.primary), "outside.md")), r); len(got) != 0 {
		t.Errorf("Edit outside every repository: actions %+v, want none", got)
	}
	// The primary on another branch is not guarded; origin/HEAD names the default.
	testiso.Git(t, w.primary, "checkout", "-q", "-b", "scratch")
	if got := decide(t, edit(w.primary, file), r); len(got) != 0 {
		t.Errorf("Edit in the primary on scratch: actions %+v, want none", got)
	}
	testiso.Git(t, w.primary, "update-ref", "refs/remotes/origin/scratch", "HEAD")
	testiso.Git(t, w.primary, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/scratch")
	if d := verdicts(decide(t, edit(w.primary, file), r), kernel.VerdictDeny); len(d) != 1 {
		t.Errorf("Edit in the primary on the default branch origin/HEAD names: denies %+v, want one", d)
	}
}

func TestSecrets_NotApplicableWithoutGitleaks(t *testing.T) {
	w := newWorld(t)
	testiso.PathWithOnly(t, "git")
	rec := testsupport.Recorder()
	got := decide(t, bash(t, w.tree, `git commit -m "fix"`, true), rec)
	want := []kernel.Action{{RowID: "secrets", Verdict: kernel.VerdictAllow, Line: "not applicable: gitleaks not installed"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("actions %+v, want %+v", got, want)
	}
}

func TestSecrets_SpawnsOnlyForACommit(t *testing.T) {
	w := newWorld(t)
	testiso.FakeTool(t, "gitleaks", 0, "")
	rec := testsupport.Recorder()
	for _, cmd := range []string{"git status", "git push origin HEAD", "echo git commit", "ls"} {
		decide(t, bash(t, w.tree, cmd, true), rec)
	}
	if rec.Count() != 0 {
		t.Fatalf("spawns %v for non-commit commands, want none", rec.Kinds())
	}
	decide(t, bash(t, w.tree, `git commit -m "x"`, true), rec)
	spec := rec.Specs()
	if len(spec) != 1 || spec[0].Name != "gitleaks" || strings.Join(spec[0].Args, " ") != "protect --staged --no-banner --redact --exit-code 2" {
		t.Errorf("spawns %+v, want one gitleaks protect --staged --no-banner --redact", spec)
	}
}

func TestDiscard_WorktreeRemoveForceWithUnstagedIsDenied(t *testing.T) {
	w := newWorld(t)
	// A tree with exactly one unstaged file, and a clean one.
	dirty := testiso.AddTree(t, w.primary, "dirty", "dirty/x")
	clean := testiso.AddTree(t, w.primary, "clean", "clean/x")
	testiso.Write(t, filepath.Join(dirty, "README.md"), "edited\n")
	r := quiet(t)

	got := decide(t, bash(t, w.primary, "git worktree remove --force "+filepath.ToSlash(dirty), true), r)
	denies := verdicts(got, kernel.VerdictDeny)
	if len(denies) != 1 || denies[0].RowID != "discard-uncommitted" || !strings.Contains(denies[0].Line, "1 unstaged file") {
		t.Fatalf("dirty tree: actions %+v, want a discard-uncommitted deny naming \"1 unstaged file\"", got)
	}
	if got := decide(t, bash(t, w.primary, "git worktree remove --force "+filepath.ToSlash(clean), true), r); len(got) != 0 {
		t.Errorf("clean tree: actions %+v, want none", got)
	}
}

func TestDiscard_SpawnsOnlyForDiscardForms(t *testing.T) {
	w := newWorld(t)
	count := testiso.CountGitSpawns(t)
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
		decide(t, bash(t, w.tree, cmd+strings.Repeat(" ", i/len(templates)), true), rec)
		n++
	}
	if rec.Count() != 0 || count() != 0 {
		t.Fatalf("%d non-discard payloads: %d Light spawns %v, %d git processes; want 0 and 0", n, rec.Count(), rec.Kinds(), count())
	}
	decide(t, bash(t, w.tree, "git reset --hard", true), rec)
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
		{"git branch -D unmerged", "branch -D unmerged discards 1 commit unreachable from any other ref"},
	}
	for _, c := range cases {
		denies := verdicts(decide(t, bash(t, w.tree, c.cmd, true), r), kernel.VerdictDeny)
		if len(denies) != 1 || !strings.Contains(denies[0].Line, ": "+c.want+".") && !strings.Contains(denies[0].Line, ": "+c.want+" ") {
			t.Errorf("%q: denies %+v, want a cause starting %q", c.cmd, denies, c.want)
		}
	}
	// A clean tree costs nothing, and a pushed branch is recoverable.
	clean := testiso.AddTree(t, w.primary, "clean", "clean/x")
	for _, cmd := range []string{"git reset --hard", "git clean -fd", "git checkout -- README.md"} {
		if got := decide(t, bash(t, clean, cmd, true), r); len(got) != 0 {
			t.Errorf("%q in a clean tree: actions %+v, want none", cmd, got)
		}
	}
	testiso.Git(t, w.primary, "update-ref", "refs/remotes/origin/unmerged", "unmerged")
	if got := decide(t, bash(t, w.tree, "git branch -D unmerged", true), r); len(got) != 0 {
		t.Errorf("branch -D of a branch a remote has: actions %+v, want none", got)
	}
}

func TestUndercover_TrailerDenied(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	msg := "fix the parser\n\nIt read past the end of the buffer."
	if got := decide(t, bash(t, w.tree, "git commit -m "+dq(msg), true), r); len(got) != 0 {
		t.Fatalf("message without a trailer: actions %+v, want none", got)
	}
	with := msg + "\n\nCo-Authored-By: X <x@y>"
	denies := verdicts(decide(t, bash(t, w.tree, "git commit -m "+dq(with), true), r), kernel.VerdictDeny)
	if len(denies) != 1 || denies[0].RowID != "undercover" || !strings.Contains(denies[0].Line, "Override: none.") {
		t.Fatalf("message with a trailer: denies %+v, want one from undercover with no override", denies)
	}
	for _, cmd := range []string{
		"git commit -am " + dq(with),
		"git commit --message=" + dq(with),
		"git commit -m" + dq(with),
		"git -C " + filepath.ToSlash(w.tree) + " commit -m " + dq("Generated with [Claude Code](https://claude.com/claude-code)"),
		"git commit -m " + dq("ask Sonnet 5.5"),
		"git commit -m \"$(cat <<'EOF'\n" + with + "\nEOF\n)\"",
		"git commit -F - <<'EOF'\n" + with + "\nEOF",
		"gh pr create --title t --body " + dq("body\n\n"+"Claude-Session: https://claude.ai/code/session_1"),
		"gh pr edit 7 --body=" + dq("🤖 Generated with Claude Code"),
	} {
		d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny)
		if len(d) != 1 || d[0].RowID != "undercover" {
			t.Errorf("%q: denies %+v, want one from undercover", cmd, d)
		}
	}
	file := filepath.Join(w.tree, "msg.txt")
	testiso.Write(t, file, with)
	if d := verdicts(decide(t, bash(t, w.tree, "git commit -F msg.txt", true), r), kernel.VerdictDeny); len(d) != 1 || d[0].RowID != "undercover" {
		t.Errorf("-F with a trailer in the file: denies %+v, want one from undercover", d)
	}
	if got := decide(t, bash(t, w.tree, "gh pr create --title t --body "+dq(msg), true), r); len(got) != 0 {
		t.Errorf("clean PR body: actions %+v, want none", got)
	}
}

func TestWallsWarnPeople_NonAgentGetsWarning(t *testing.T) {
	w := newWorld(t)
	testiso.FakeTool(t, "gitleaks", 2, "leaks found: 1")
	rec := testsupport.Recorder()

	// main-commit, as an agent: a deny. Without a session_id: a warn.
	cmd := "git -C " + filepath.ToSlash(w.primary) + " push origin main"
	agent := decide(t, bash(t, w.tree, cmd, true), rec)
	if d := verdicts(agent, kernel.VerdictDeny); len(d) != 1 || d[0].RowID != "main-commit" {
		t.Fatalf("agent: actions %+v, want a main-commit deny", agent)
	}
	person := decide(t, bash(t, w.tree, cmd, false), rec)
	if wn := verdicts(person, kernel.VerdictWarn); len(wn) != 1 || wn[0].RowID != "main-commit" || len(verdicts(person, kernel.VerdictDeny)) != 0 {
		t.Errorf("person: actions %+v, want one main-commit warn and no deny", person)
	}

	// secrets is a deny for people too.
	commit := decide(t, bash(t, w.tree, `git commit -m "add keys"`, false), rec)
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
	ctx := kernel.Context{Tool: "Bash", Args: map[string]string{"cwd": w.tree, "command": "ls"}}
	for _, row := range walls.Rows() {
		if a, ok := walls.Match(row, ctx, testsupport.Recorder()); ok {
			t.Errorf("Match(%s, ls) = %+v, true; want no match", row.ID, a)
		}
	}
}

func TestSecrets_OnlyExitCodeTwoIsAFinding(t *testing.T) {
	w := newWorld(t)
	testiso.FakeTool(t, "gitleaks", 1, "fatal: not a git repository")
	got := decide(t, bash(t, w.tree, `git commit -m "x"`, true), testsupport.Recorder())
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
		testiso.FakeTool(t, "gitleaks", 0, "")
		rec := testsupport.Recorder()
		decide(t, bash(t, w.tree, c.cmd, true), rec)
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

// failing is a Runner whose every child fails, as a broken git or a timeout does.
type failing struct{}

func (failing) Light(run.Spec) (run.Result, error) { return run.Result{}, run.ErrTimeout }

func TestDiscard_AMeasurementThatFailsIsAWarningNotADeny(t *testing.T) {
	w := newWorld(t)
	for _, cmd := range []string{"git reset --hard", "git clean -fd", "git worktree remove --force " + filepath.ToSlash(w.tree)} {
		got := decide(t, bash(t, w.primary, cmd, true), failing{})
		if len(got) != 1 || got[0].Verdict != kernel.VerdictWarn || got[0].RowID != "discard-uncommitted" ||
			!strings.Contains(got[0].Line, "could not measure") {
			t.Errorf("%q with a failing git: actions %+v, want one discard-uncommitted warn naming \"could not measure\"", cmd, got)
		}
	}
}

func TestDiscard_BranchDeleteCountsCommitsNoOtherRefReaches(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	// merged: the tip is HEAD's. A second local branch holds the same commits as feat.
	testiso.Git(t, w.primary, "branch", "merged")
	testiso.Git(t, w.primary, "branch", "feat", "unmerged")
	if got := decide(t, bash(t, w.tree, "git branch -D merged", true), r); len(got) != 0 {
		t.Errorf("branch -D of a merged branch: actions %+v, want none", got)
	}
	if got := decide(t, bash(t, w.tree, "git branch -D feat", true), r); len(got) != 0 {
		t.Errorf("branch -D of a branch another local branch holds: actions %+v, want none", got)
	}
	testiso.Git(t, w.primary, "branch", "-D", "feat")
	if d := verdicts(decide(t, bash(t, w.tree, "git branch -D unmerged", true), r), kernel.VerdictDeny); len(d) != 1 {
		t.Errorf("branch -D of the only holder of a commit: denies %+v, want one", d)
	}
}

// ps is a PreToolUse payload of a PowerShell call in dir.
func ps(t testing.TB, dir, command string) []byte {
	t.Helper()
	return testiso.HookPayload(t, kernel.HookPreToolUse, kernel.ToolPowerShell, dir, map[string]string{"command": command}, testiso.WithSession("s-1"))
}

func TestPowerShell_NativePathsReachTheWalls(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	// On Windows these paths hold backslashes, which bash would read as escapes.
	for _, c := range []struct{ cmd, row string }{
		{"git -C " + w.primary + " push origin main", "main-commit"},
		{"Set-Content " + filepath.Join(w.primary, "x.txt") + " hi", "primary-write"},
		{"Set-Content -Path '" + filepath.Join(w.primary, "x.txt") + "' -Value hi", "primary-write"},
		{"hi | Out-File " + filepath.Join(w.primary, "y.txt"), "primary-write"},
		{"echo hi > " + filepath.Join(w.primary, "z.txt"), "primary-write"},
		{"Set-Location " + w.tree + "; git reset --hard", "discard-uncommitted"},
	} {
		denies := verdicts(decide(t, ps(t, w.tree, c.cmd), r), kernel.VerdictDeny)
		if len(denies) != 1 || denies[0].RowID != c.row {
			t.Errorf("PowerShell %q: denies %+v, want exactly one from %s", c.cmd, denies, c.row)
		}
	}
}

func TestWalls_CommentsAndMaskedTextNeverSplitOrMatch(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	for _, cmd := range []string{
		`git status # note; git reset --hard`,
		`echo '$(git reset --hard)'`,
		"echo ok # x\n# git reset --hard\nls",
	} {
		if got := decide(t, bash(t, w.tree, cmd, true), r); len(got) != 0 {
			t.Errorf("Bash %q: actions %+v, want none", cmd, got)
		}
	}
	for _, cmd := range []string{
		"git status <# ; git reset --hard #>",
		"@\"\ngit reset --hard \"x\"\n\"@",
		"Write-Host 'a; git reset --hard'",
	} {
		if got := decide(t, ps(t, w.tree, cmd), r); len(got) != 0 {
			t.Errorf("PowerShell %q: actions %+v, want none", cmd, got)
		}
	}
	// The same words in code do deny.
	if d := verdicts(decide(t, bash(t, w.tree, "git status; git reset --hard", true), r), kernel.VerdictDeny); len(d) != 1 {
		t.Errorf("git status; git reset --hard: denies %+v, want one", d)
	}
	if d := verdicts(decide(t, bash(t, w.tree, "echo $(git reset --hard)", true), r), kernel.VerdictDeny); len(d) != 1 {
		t.Errorf("echo $(git reset --hard): denies %+v, want one", d)
	}
}

func TestWalls_AnUnreadableRemoteDoesNotOpenTheMainCommitWall(t *testing.T) {
	w := newWorld(t)
	cfg := filepath.Join(w.primary, ".git", "config")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A line over bufio.Scanner's 64KB limit makes the remote read fail.
	_, werr := f.WriteString("[remote \"origin\"]\n\turl = " + strings.Repeat("x", 70000) + "\n")
	if cerr := f.Close(); werr != nil || cerr != nil {
		t.Fatal(werr, cerr)
	}
	denies := verdicts(decide(t, bash(t, w.primary, `git commit -m "wip"`, true), quiet(t)), kernel.VerdictDeny)
	if len(denies) != 1 || denies[0].RowID != "main-commit" {
		t.Errorf("denies %+v, want main-commit: the config read failed but the repo is still known", denies)
	}
}

func TestPrimaryWrite_ExemptsIgnoredPathsAndTheGitDir(t *testing.T) {
	w := newWorld(t)
	testiso.Write(t, filepath.Join(w.primary, ".gitignore"), "docs/sdd/\n*.log\n")
	r := quiet(t)
	edit := func(file string) []byte {
		return testiso.HookPayload(t, kernel.HookPreToolUse, kernel.ToolEdit, w.primary, map[string]string{"file_path": file}, testiso.WithSession("s"))
	}
	for _, c := range []struct {
		file string
		deny bool
	}{
		{filepath.Join(w.primary, "docs", "sdd", "plan.md"), false},
		{filepath.Join(w.primary, "build.log"), false},
		{filepath.Join(w.primary, ".git", "info", "exclude"), false},
		{filepath.Join(w.primary, ".git", "hooks", "pre-commit"), true},
		{filepath.Join(w.primary, ".git", "config"), true},
		{filepath.Join(w.primary, ".git", "refs", "heads", "main"), true},
		{filepath.Join(w.primary, ".git", "HEAD"), true},
		{filepath.Join(w.primary, "docs", "a.md"), true},
		{filepath.Join(w.primary, "README.md"), true},
	} {
		d := verdicts(decide(t, edit(c.file), r), kernel.VerdictDeny)
		if got := len(d) == 1 && d[0].RowID == "primary-write"; got != c.deny {
			t.Errorf("Edit of %s: denies %+v, want deny=%v", c.file[len(w.primary):], d, c.deny)
		}
	}
	// A shell write is judged the same way.
	if d := verdicts(decide(t, bash(t, w.primary, "echo x > docs/sdd/notes.md", true), r), kernel.VerdictDeny); len(d) != 0 {
		t.Errorf("shell write to an ignored path: denies %+v, want none", d)
	}
	if d := verdicts(decide(t, bash(t, w.primary, "echo x > notes.md", true), r), kernel.VerdictDeny); len(d) != 1 {
		t.Errorf("shell write to a tracked-to-be path: denies %+v, want one", d)
	}
}

func TestMainCommit_FollowsGitDirAndWorkTreeToTheRepoTheyName(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	gitDir := filepath.ToSlash(filepath.Join(w.primary, ".git"))
	primary := filepath.ToSlash(w.primary)
	for _, cmd := range []string{
		`git --git-dir=` + gitDir + ` commit -m x`,
		`git --git-dir ` + gitDir + ` --work-tree ` + primary + ` commit -m x`,
		`git --git-dir="` + gitDir + `" push origin main`,
		`GIT_DIR=` + gitDir + ` git commit -m x`,
		`cd ` + primary + ` && git commit -m x`,
	} {
		d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny)
		if len(d) != 1 || d[0].RowID != "main-commit" {
			t.Errorf("%q from a tree: denies %+v, want main-commit", cmd, d)
		}
	}
	// A git dir of the tree itself, named in full, is the tree: no deny.
	treeGit := filepath.ToSlash(filepath.Join(w.tree, ".git"))
	if d := verdicts(decide(t, bash(t, w.tree, `git --git-dir=`+treeGit+` commit --dry-run -m x`, true), r), kernel.VerdictDeny); len(d) != 0 {
		t.Errorf("a tree's own --git-dir: denies %+v, want none", d)
	}
}

func TestMainCommit_FollowsAnExportedGitDir(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	gitDir := filepath.ToSlash(filepath.Join(w.primary, ".git"))
	for _, cmd := range []string{
		`export GIT_DIR=` + gitDir + `; git commit -m x`,
		`export A=1 GIT_DIR=` + gitDir + ` && git push origin main`,
	} {
		d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny)
		if len(d) != 1 || d[0].RowID != "main-commit" {
			t.Errorf("%q from a tree: denies %+v, want main-commit", cmd, d)
		}
	}
	if d := verdicts(decide(t, bash(t, w.tree, `export GIT_DIR=`+gitDir+`; echo hi; git status`, true), r), kernel.VerdictDeny); len(d) != 0 {
		t.Errorf("an exported GIT_DIR with a harmless git verb: denies %+v, want none", d)
	}
}

// With a squash merge the PR title is the subject on main, so the title, the
// squash subject and a merge commit's message are read like a commit message.
func TestUndercover_PRTitleSubjectAndMergeMessageAreRead(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	bad := "fix the parser, written by Claude"
	for _, cmd := range []string{
		"gh pr create --title " + dq(bad) + " --body ok",
		"gh pr create -t " + dq(bad) + " --body ok",
		"gh pr create --title=" + dq(bad),
		"gh pr edit 7 --title " + dq(bad),
		"gh pr merge 7 --squash --subject " + dq(bad),
		"gh pr merge 7 --squash --body " + dq("ok\n\nCo-Authored-By: X <x@y>"),
		"gh pr merge 7 -t " + dq(bad),
		"git merge -m " + dq(bad) + " other",
		"git merge --message=" + dq(bad) + " other",
		"git merge --no-ff other -m " + dq("Co-Authored-By: X <x@y>"),
	} {
		d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny)
		if len(d) != 1 || d[0].RowID != "undercover" {
			t.Errorf("%q: denies %+v, want one from undercover", cmd, d)
		}
	}
	for _, cmd := range []string{
		"gh pr create --title " + dq("fix the parser") + " --body ok",
		"gh pr merge 7 --squash --subject " + dq("fix the parser"),
		"gh pr merge 7 --squash --delete-branch",
		"git merge -m " + dq("merge other") + " other",
		"git merge other",
	} {
		if d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny); len(d) != 0 {
			t.Errorf("%q: denies %+v, want none", cmd, d)
		}
	}
}

// A vendor's address is attribution wherever it appears: on a line of the
// message, as --author, or as the identity a -c sets. A name alone is not.
func TestUndercover_VendorAddressesInLinesAndIdentities(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	for _, cmd := range []string{
		"git commit -m " + dq("fix\n\nreviewed via noreply@anthropic.com"),
		"git commit -m " + dq("fix\n\nSigned-off-by: Jane <jane@anthropic.com>"),
		"git commit -m " + dq("fix\n\nfrom cursoragent@cursor.com"),
		"git commit -m " + dq("fix\n\nReply-To: noreply@openai.com"),
		"git commit -m " + dq("fix\n\n175728472+Copilot@users.noreply.github.com"),
		"git commit -m x --author=" + dq("Claude <noreply@anthropic.com>"),
		"git commit -m x --author " + dq("Cursor Agent <cursoragent@cursor.com>"),
		"git commit -m x --author=" + dq("Copilot <175728472+copilot@users.noreply.github.com>"),
		"git -c user.email=noreply@anthropic.com commit -m x",
		"git -c user.name=Bot -c user.email=noreply@openai.com commit -m x",
		"git -c user.email=cursoragent@cursor.com commit --amend --no-edit",
	} {
		d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny)
		if len(d) != 1 || d[0].RowID != "undercover" {
			t.Errorf("%q: denies %+v, want one from undercover", cmd, d)
		}
	}
	for _, cmd := range []string{
		"git commit -m " + dq("merge commits come from noreply@github.com"),
		"git commit -m x --author=" + dq("Claude Monet <claude.monet@example.com>"),
		"git commit -m x --author " + dq("Jane Doe <jane@example.com>"),
		"git -c user.name=Claude -c user.email=claude.shannon@bell-labs.com commit -m x",
		"git -c user.email=12345+jane@users.noreply.github.com commit -m x",
		"git -c core.pager=cat log -1",
	} {
		if d := verdicts(decide(t, bash(t, w.tree, cmd, true), r), kernel.VerdictDeny); len(d) != 0 {
			t.Errorf("%q: denies %+v, want none", cmd, d)
		}
	}
}

// git clean -x also deletes what .gitignore hides (.env, build output), -X only
// that; the measurement counts the ignored files git status lists as !!.
func TestDiscard_CleanXMeasuresIgnoredFiles(t *testing.T) {
	w := newWorld(t)
	r := quiet(t)
	tree := testiso.AddTree(t, w.primary, "ign", "ign/x")
	testiso.Write(t, filepath.Join(tree, ".gitignore"), ".env\n")
	testiso.Git(t, tree, "add", ".gitignore")
	testiso.Git(t, tree, "commit", "-qm", "ignore .env")
	testiso.Write(t, filepath.Join(tree, ".env"), "SECRET=1\n")

	denyLine := func(cmd string) string {
		d := verdicts(decide(t, bash(t, tree, cmd, true), r), kernel.VerdictDeny)
		if len(d) != 1 {
			return ""
		}
		return d[0].Line
	}
	for _, cmd := range []string{"git clean -fd", "git clean -fd --exclude=keep", "git clean -fdn", "git clean -ndx"} {
		if got := decide(t, bash(t, tree, cmd, true), r); len(got) != 0 {
			t.Errorf("%q with only an ignored file: actions %+v, want none", cmd, got)
		}
	}
	for _, c := range []struct{ cmd, want string }{
		{"git clean -fdx", "clean -fdx discards 1 ignored file"},
		{"git clean -fdX", "clean -fdX discards 1 ignored file"},
		{"git clean -f -x", "clean -f -x discards 1 ignored file"},
	} {
		if got := denyLine(c.cmd); !strings.Contains(got, ": "+c.want+". Override: ") {
			t.Errorf("%q: deny line %q, want it to say %q", c.cmd, got, c.want)
		}
	}
	testiso.Write(t, filepath.Join(tree, "new.txt"), "n\n")
	for _, c := range []struct{ cmd, want string }{
		{"git clean -fd", "clean -fd discards 1 untracked file"},
		{"git clean -fdx", "clean -fdx discards 1 untracked file, 1 ignored file"},
		{"git clean -fdX", "clean -fdX discards 1 ignored file"},
	} {
		if got := denyLine(c.cmd); !strings.Contains(got, ": "+c.want+". Override: ") {
			t.Errorf("%q: deny line %q, want it to say %q", c.cmd, got, c.want)
		}
	}
}
