package hook

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/engine"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/run"
	"github.com/harryberg1n/trellis/internal/store"
)

// Deps is what a hook needs from the outside.
type Deps struct {
	Store  *store.Store
	Runner run.Runner
	Clock  clock.Clock
	Rows   []kernel.Row
	// Err says why Store is nil: the command could not open it. Run records it
	// as an internal.error.
	Err error
	// Fallback is a directory used as a store of last resort. An internal.error
	// that the main store cannot take is parked in Fallback/events.pending, to
	// be folded in once the real store works again. "" means no fallback.
	Fallback string
	// Repo is the repository the caller already located from the payload's
	// cwd; parse then does not locate it again. nil means locate it.
	Repo *objects.Repo
}

// Run reads one payload from stdin and records it. It returns the exit code,
// and the code is always 0: a recorder that fails must not block the agent.
// On PreToolUse it evaluates the walls (see preToolUse) and exits 2 for a deny;
// on every other event it writes nothing to stdout or stderr.
// Any failure, a panic included, is appended as an internal.error event, or
// parked in the fallback store when the main one cannot take it.
//
// Durability: edits are written lazily (no fsync). Stop and SessionEnd append
// and then Sync the log and checkpoint. Blobs, including those AnchorTurn
// writes, are not fsynced, so a power loss can lose them; the next Stop
// re-anchors from the working tree, so the loss window is the current turn.
func Run(event string, stdin io.Reader, stdout, stderr io.Writer, deps Deps) (code int) {
	var p parsed
	defer func() {
		if r := recover(); r != nil {
			fail(deps, event, p, fmt.Errorf("hook: panic: %v", r))
			code = 0 // a hook that fails lets the call through
		}
	}()
	raw, err := ReadPayload(stdin)
	if err != nil {
		fail(deps, event, p, err)
		return 0
	}
	if p, err = parse(event, raw, deps.Repo); err != nil {
		fail(deps, event, p, err)
		return 0
	}
	if name, _ := canonical(event); name == kernel.HookPreToolUse {
		return preToolUse(deps, event, p, stdout, stderr)
	}
	if p.ev.Kind == "" {
		return 0
	}
	if err := record(deps, &p); err != nil {
		fail(deps, event, p, err)
	}
	return 0
}

// record performs the event's effect and appends the event.
func record(deps Deps, p *parsed) error {
	ev := p.ev
	ev.Detail = maps.Clone(ev.Detail)
	if ev.Detail == nil {
		ev.Detail = map[string]string{}
	}
	switch {
	case ev.Kind == kernel.EvEdit && p.file != "":
		blob, err := objects.WriteBlob(p.repo.GitDir, p.file)
		if err != nil {
			return fmt.Errorf("hook: blob for %s: %w", ev.Detail[kernel.DetailPath], err)
		}
		ev.Detail["blob"] = blob
	case ev.Kind == kernel.EvTurnAnchored:
		if deps.Runner == nil {
			return errors.New("hook: no runner to anchor the turn with")
		}
		// Status is the truth for which files the turn changed, so no edit-time
		// blobs are passed in: their objects are already stored and WriteBlob
		// finds them, while a blob for a file the turn later reverted or that
		// is ignored would put a stale entry in the tree.
		tree, commit, err := objects.AnchorTurn(deps.Runner, p.repo.Root, ev.Lane, "", nil)
		if err != nil {
			return fmt.Errorf("hook: anchoring the turn: %w", err)
		}
		ev.Detail["tree"], ev.Detail["commit"], ev.Detail["ref"] = tree, commit, objects.RefFor(ev.Lane)
	}
	if len(ev.Detail) == 0 {
		ev.Detail = nil
	}
	if deps.Store == nil {
		return storeMissing(deps)
	}
	ev.At = now(deps)
	// Edits are lazy: no fsync on the edit path. The turn boundary is where the
	// log and checkpoint are made durable, and Stop re-anchors the tree from
	// disk. Stop and SessionEnd append lazily and then Sync, which is the same
	// durability as a durable append with half the fsyncs.
	boundary := ev.Kind == kernel.EvTurnAnchored || ev.Kind == kernel.EvSessionUsage
	if _, err := engine.Step(deps.Store, deps.Rows, ev, p.ctx, engine.WithDurability(store.Lazy)); err != nil {
		return fmt.Errorf("hook: recording %s: %w", ev.Kind, err)
	}
	if boundary {
		if err := deps.Store.Sync(); err != nil {
			return fmt.Errorf("hook: syncing after %s: %w", ev.Kind, err)
		}
	}
	return nil
}

func storeMissing(deps Deps) error {
	if deps.Err != nil {
		return fmt.Errorf("hook: no store: %w", deps.Err)
	}
	return errors.New("hook: no store")
}

func now(deps Deps) time.Time {
	if deps.Clock == nil {
		return clock.Real().Now()
	}
	return deps.Clock.Now()
}

// fail records err as an internal.error. It tries the main store first, so the
// error lands beside the events it concerns; if that fails too, the event is
// parked in the fallback store. A failure of the fallback is dropped: there is
// nowhere left to write, and the agent must go on.
func fail(deps Deps, event string, p parsed, err error) {
	// Nothing here may escape: a panic while recording a failure is dropped.
	defer func() { _ = recover() }()
	ev := kernel.Event{
		Kind:    kernel.EvInternalError,
		Lane:    p.ev.Lane,
		Session: p.ev.Session,
		At:      now(deps),
		Detail:  map[string]string{"error": err.Error(), "event": event},
	}
	if p.ev.Kind != "" {
		ev.Detail["recording"] = p.ev.Kind
	}
	if deps.Store != nil {
		aerr := appendGuarded(deps.Store, ev)
		if aerr == nil || errors.Is(aerr, store.ErrParked) {
			return
		}
		ev.Detail["store_error"] = aerr.Error()
	} else if deps.Err != nil {
		ev.Detail["store_error"] = deps.Err.Error()
	}
	if deps.Fallback == "" {
		return
	}
	clk := deps.Clock
	if clk == nil {
		clk = clock.Real()
	}
	if s, oerr := store.Open(deps.Fallback, clk); oerr == nil {
		_ = s.Park(ev)
	}
}

// appendGuarded is Store.Append with a panic turned into an error, so the
// fallback still gets the event.
func appendGuarded(s *store.Store, ev kernel.Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("store panicked: %v", r)
		}
	}()
	_, err = s.Append(ev)
	return err
}
