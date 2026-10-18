// Package walls decides PreToolUse tool calls against the wall and policy rows
// of the feature table. A wall is a rule with an incident behind it; a policy
// is a rule with an owner decision behind it. The kernel holds the rows and the
// pure evaluation; the matching lives here because it reads the repository and
// may start one light process.
//
// The deny line is "trellis: <row-id>: <cause>. Override: <command>." where the
// command is `trellis allow <row-id>`, or "none" for a row with no override.
package walls

import (
	"slices"
	"strconv"
	"strings"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/run"
)

// ErrOwnGitState is what run.Light reports for a git child that would write a
// ref outside refs/trellis/ or a lane's index: the own-git-state wall. It is
// checked at the run.Light seam, not in a hook, so Match never matches that
// row.
var ErrOwnGitState = run.ErrOwnGitState

// The row ids, as kernel.Table spells them.
const (
	PrimaryWrite       = "primary-write"
	MainCommit         = "main-commit"
	HookBypass         = "hook-bypass"
	DiscardUncommitted = "discard-uncommitted"
	OwnGitState        = "own-git-state"
	Secrets            = "secrets"
	Undercover         = "undercover"
	WallsWarnPeople    = "walls-warn-people"
	SecretsEveryone    = "secrets-everyone"
)

// Rows is the wall and policy rows of the shipped table.
func Rows() []kernel.Row { return slices.Clone(kernel.Table) }

// Overridable reports whether `trellis allow <id>` may waive the row. The
// secrets and undercover rows have no override, the own-git-state wall is not
// a hook's to waive, and the policies are not walls.
func Overridable(id string) bool {
	switch id {
	case PrimaryWrite, MainCommit, HookBypass, DiscardUncommitted:
		return true
	}
	return false
}

// DenyLine is the line a row's refusal prints.
func DenyLine(id, cause string) string {
	override := "none"
	if Overridable(id) {
		override = "trellis allow " + id
	}
	return "trellis: " + id + ": " + cause + ". Override: " + override + "."
}

// analysis is one tool call, read once for every row that asks about it.
type analysis struct {
	tool    string
	cwd     string
	command string
	file    string
	r       run.Runner

	callsDone bool
	calls     []gitCall
	targets   []string
	targetsOK bool
}

func analyze(ctx kernel.Context, r run.Runner) *analysis {
	return &analysis{
		tool: ctx.Tool, cwd: ctx.Args[kernel.ArgCwd], command: ctx.Args[kernel.ArgCommand], file: ctx.Args[kernel.ArgFilePath], r: r,
	}
}

// isShell reports whether the tool runs a command line.
func (a *analysis) isShell() bool {
	return kernel.IsShellTool(a.tool) && a.command != ""
}

// gitCalls is every git invocation the command runs.
func (a *analysis) gitCalls() []gitCall {
	if a.callsDone {
		return a.calls
	}
	a.callsDone = true
	if !a.isShell() || !strings.Contains(a.command, "git") {
		return nil
	}
	walk(a.tool, a.command, a.cwd, false, func(seg []word, dir string) {
		if c, ok := parseGit(seg, dir); ok {
			a.calls = append(a.calls, c)
		}
	})
	return a.calls
}

// writes is every path the command writes, absolute.
func (a *analysis) writes() []string {
	if a.targetsOK {
		return a.targets
	}
	a.targetsOK = true
	if !a.isShell() {
		return nil
	}
	walk(a.tool, a.command, a.cwd, false, func(seg []word, dir string) {
		a.targets = append(a.targets, writeTargets(seg, dir)...)
	})
	return a.targets
}

// Match judges one row on one tool call. It returns the row's action and true,
// or false when the row has nothing to say. A deny action carries the deny
// line; a row that cannot act (secrets with no gitleaks) returns an allow
// action carrying why. r runs the one light process a row may start.
func Match(row kernel.Row, ctx kernel.Context, r run.Runner) (kernel.Action, bool) {
	return match(row, analyze(ctx, r))
}

func match(row kernel.Row, a *analysis) (kernel.Action, bool) {
	var cause string
	switch row.ID {
	case PrimaryWrite:
		cause = primaryWrite(a)
	case MainCommit:
		cause = mainCommit(a)
	case HookBypass:
		cause = hookBypass(a)
	case DiscardUncommitted:
		var unmeasured string
		if cause, unmeasured = discardUncommitted(a); cause == "" && unmeasured != "" {
			return kernel.Action{RowID: row.ID, Verdict: kernel.VerdictWarn, Line: "trellis: " + row.ID + ": " + unmeasured + "."}, true
		}
	case Secrets:
		return secrets(a)
	case Undercover:
		cause = undercover(a)
	}
	if cause == "" {
		return kernel.Action{}, false
	}
	return kernel.Action{RowID: row.ID, Verdict: kernel.VerdictDeny, Line: DenyLine(row.ID, cause)}, true
}

// Decide evaluates the rows on one tool call: kernel.DecideWith with Match as
// the matcher, then the two policies that change a verdict. With no session
// (a person at a git hook, not an agent) walls-warn-people turns each wall's
// deny into a warn, and secrets-everyone keeps the secrets wall a deny.
func Decide(rows []kernel.Row, ev kernel.Event, ctx kernel.Context, r run.Runner) []kernel.Action {
	a := analyze(ctx, r)
	actions := kernel.DecideWith(rows, ev, ctx, func(row kernel.Row, _ kernel.Event, _ kernel.Context) (kernel.Action, bool) {
		return match(row, a)
	})
	if ctx.Args[kernel.ArgPerson] != "1" || !present(rows, WallsWarnPeople) {
		return actions
	}
	for i, act := range actions {
		// An id no row has is left as it is: only a known wall softens.
		if row, ok := kernel.Find(rows, act.RowID); act.Verdict != kernel.VerdictDeny || !ok || row.Kind != kernel.Wall {
			continue
		}
		if act.RowID == Secrets && present(rows, SecretsEveryone) {
			continue
		}
		actions[i].Verdict = kernel.VerdictWarn
	}
	return actions
}

// present reports whether the table has the row and it acts.
func present(rows []kernel.Row, id string) bool {
	r, ok := kernel.Find(rows, id)
	return ok && (r.State == kernel.Admitted || r.State == kernel.LiveAB)
}

// onDefault reports whether this is the primary checkout with the default
// branch checked out.
func onDefault(r objects.Repo) bool { return r.Primary && r.Branch == r.Default }

// repoAt reads the repository around dir without starting a process. ok is
// false when dir is relative, unknown, or in no repository.
func repoAt(dir string) (objects.Repo, bool) {
	if dir == "" {
		return objects.Repo{}, false
	}
	r, err := objects.Open(dir)
	return r, err == nil
}

// plural is "1 unstaged file" or "2 unstaged files".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
