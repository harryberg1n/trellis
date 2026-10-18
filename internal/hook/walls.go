package hook

import (
	"encoding/json"
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
// bytes. When every deny of the call has a valid one-shot `trellis allow`
// token, all the tokens are consumed together and the call is let through,
// with one wall.overridden per row; if any deny lacks a token none is consumed.
// A warn, and an allow that carries a line (a wall that cannot act), exit 0 and
// reach the agent as additionalContext in the hook's JSON on stdout: unlike
// permissionDecision "allow", that does not skip the permission prompt. A warn
// or an allow records nothing. Recording failures become internal.error
// events; they never lift a deny.
func preToolUse(deps Deps, event string, p parsed, stdout, stderr io.Writer) int {
	runner := deps.Runner
	if runner == nil {
		clk := deps.Clock
		if clk == nil {
			clk = clock.Real()
		}
		runner = run.Exec{Clock: clk}
	}
	var denies []kernel.Action
	var context []string
	for _, a := range walls.Decide(deps.Rows, p.ev, p.ctx, runner) {
		switch {
		case a.Verdict == kernel.VerdictDeny:
			denies = append(denies, a)
		case a.Verdict == kernel.VerdictWarn || a.Verdict == kernel.VerdictAllow && a.Line != "":
			context = append(context, a.Line)
		}
	}
	if len(denies) > 0 && waivedAll(deps, event, p, denies) {
		for _, a := range denies {
			note(deps, event, p, kernel.EvWallOverridden, map[string]string{"row": a.RowID})
		}
		denies = nil
	}
	if len(denies) > 0 {
		lines := make([]string, 0, len(denies))
		for _, a := range denies {
			kind := kernel.EvWallDenied
			if row, ok := kernel.Find(deps.Rows, a.RowID); ok && row.Kind == kernel.Policy {
				kind = kernel.EvPolicyDenied
			}
			note(deps, event, p, kind, map[string]string{"row": a.RowID, "line": a.Line, "bytes": strconv.Itoa(len(a.Line))})
			lines = append(lines, a.Line)
		}
		_, _ = io.WriteString(stderr, strings.Join(lines, "\n")+"\n")
		return exitDeny
	}
	if len(context) > 0 {
		_, _ = io.WriteString(stdout, contextJSON(context)+"\n")
	}
	return 0
}

// contextJSON is the PreToolUse output that adds lines to the agent's context.
func contextJSON(lines []string) string {
	type output struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	b, err := json.Marshal(struct {
		HookSpecificOutput output `json:"hookSpecificOutput"`
	}{output{HookEventName: "PreToolUse", AdditionalContext: strings.Join(lines, "\n")}})
	if err != nil { // strings always marshal
		return "{}"
	}
	return string(b)
}

// waivedAll consumes the one-shot tokens of every denied row, or none: it
// reports false unless every deny is overridable and has a valid token.
func waivedAll(deps Deps, event string, p parsed, denies []kernel.Action) bool {
	if deps.Store == nil {
		return false
	}
	ids := make([]string, 0, len(denies))
	for _, a := range denies {
		if !walls.Overridable(a.RowID) {
			return false
		}
		ids = append(ids, a.RowID)
	}
	ok, err := deps.Store.TakeAllows(ids...)
	if err != nil {
		fail(deps, event, p, fmt.Errorf("hook: reading the overrides: %w", err))
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
