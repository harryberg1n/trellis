package hook

import (
	"fmt"
	"io"
	"maps"
	"strconv"
	"strings"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/engine"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/walls"
)

// Exit codes of a PreToolUse hook: 2 blocks the tool call and shows stderr to
// the agent as the reason; 0 lets it through.
const exitDeny = 2

// preToolUse evaluates the walls on one tool call.
//
// A deny prints its line to stderr and exits 2, and is recorded as wall.denied
// (policy.denied for a Policy row) with the row, the line and its size in
// bytes. A deny the row's one-shot `trellis allow` token waives is let through
// and recorded as wall.overridden. A warn exits 0 and reaches the agent as
// additionalContext in the hook's JSON on stdout: unlike permissionDecision
// "allow", it does not skip the permission prompt. An allow prints nothing.
// Recording failures become internal.error events; they never lift a deny.
func preToolUse(deps Deps, event string, p parsed, stdout, stderr io.Writer) int {
	runner := deps.Runner
	if runner == nil {
		clk := deps.Clock
		if clk == nil {
			clk = clock.Real()
		}
		runner = run.Exec{Clock: clk}
	}
	var denies, warns []string
	for _, a := range walls.Decide(deps.Rows, p.ev, p.ctx, runner) {
		switch a.Verdict {
		case kernel.VerdictWarn:
			warns = append(warns, a.Line)
		case kernel.VerdictDeny:
			if walls.Overridable(a.RowID) && waived(deps, event, p, a.RowID) {
				note(deps, event, p, kernel.EvWallOverridden, map[string]string{"row": a.RowID})
				continue
			}
			kind := kernel.EvWallDenied
			if rowKind(deps.Rows, a.RowID) == kernel.Policy {
				kind = kernel.EvPolicyDenied
			}
			note(deps, event, p, kind, map[string]string{"row": a.RowID, "line": a.Line, "bytes": strconv.Itoa(len(a.Line))})
			denies = append(denies, a.Line)
		}
	}
	if len(denies) > 0 {
		_, _ = io.WriteString(stderr, strings.Join(denies, "\n")+"\n")
		return exitDeny
	}
	if len(warns) > 0 {
		_, _ = fmt.Fprintf(stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":%s}}`+"\n",
			strconv.Quote(strings.Join(warns, "\n")))
	}
	return 0
}

func rowKind(rows []kernel.Row, id string) kernel.Kind {
	for _, r := range rows {
		if r.ID == id {
			return r.Kind
		}
	}
	return kernel.Wall
}

// waived consumes the row's one-shot token, if there is one.
func waived(deps Deps, event string, p parsed, id string) bool {
	if deps.Store == nil {
		return false
	}
	ok, err := deps.Store.TakeAllow(id)
	if err != nil {
		fail(deps, event, p, fmt.Errorf("hook: reading the %s override: %w", id, err))
		return false
	}
	return ok
}

// note appends a wall event; a failure is an internal.error, not a lifted wall.
func note(deps Deps, event string, p parsed, kind string, detail map[string]string) {
	ev := p.ev
	ev.Kind, ev.Detail = kind, maps.Clone(detail)
	ev.At = now(deps)
	if deps.Store == nil {
		fail(deps, event, p, storeMissing(deps))
		return
	}
	if _, err := engine.Step(deps.Store, deps.Rows, ev, p.ctx); err != nil {
		fail(deps, event, p, fmt.Errorf("hook: recording %s: %w", kind, err))
	}
}
