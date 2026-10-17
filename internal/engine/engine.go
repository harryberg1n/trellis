// Package engine runs one event through the kernel under the repo lock.
package engine

import (
	"errors"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/store"
)

// Step records ev and returns the actions the rows give it. Under the repo
// lock it loads the checkpoint, folds in parked events, stamps ev.Arms from
// the lane sticky record, runs kernel.Decide, appends, saves and unlocks.
// The caller runs the returned actions outside the lock. If the lock is not
// free in time the event is parked, never dropped, and Step returns the
// actions for the event as given.
func Step(s *store.Store, rows []kernel.Row, ev kernel.Event, ctx kernel.Context) ([]kernel.Action, error) {
	var actions []kernel.Action
	err := s.Locked(func(tx *store.Tx) error {
		cp, err := tx.Checkpoint()
		if err != nil {
			return err
		}
		if cp.Arms == nil {
			cp.Arms = map[string]map[string]string{}
		}
		if cp.Lanes == nil {
			cp.Lanes = map[string]kernel.LaneState{}
		}
		// Sticky is unsynchronised; it lives and dies inside this lock.
		sticky := kernel.NewSticky()
		for lane, arms := range cp.Arms {
			sticky.Observe(kernel.Event{Lane: lane, Arms: arms})
		}
		// A save can fail after its append, so the log may be ahead of the
		// checkpoint. Catch up before deciding; replayed events keep their
		// logged arms.
		var missed []kernel.Event
		if cp.LastSeq != tx.Tail() { // equal: nothing to read
			if missed, err = s.Read(cp.LastSeq + 1); err != nil {
				return err
			}
		}
		for _, e := range missed {
			cp.Lanes[e.Lane], _ = kernel.Advance(cp.Lanes[e.Lane], e)
			sticky.Observe(e)
			cp.Arms[e.Lane] = sticky.Resolve(e.Lane, nil)
			cp.LastSeq = e.Seq
		}
		stamp := func(e kernel.Event) kernel.Event {
			state, e := kernel.Advance(cp.Lanes[e.Lane], e)
			cp.Lanes[e.Lane] = state
			sticky.Observe(e)
			arms := sticky.Resolve(e.Lane, s.Shares)
			cp.Arms[e.Lane] = arms
			e.Arms = arms
			return e
		}
		if _, err := tx.FoldPending(stamp); err != nil {
			return err
		}
		ev = stamp(ev)
		actions = kernel.Decide(rows, ev, ctx)
		if _, err := tx.Append(ev); err != nil {
			return err
		}
		cp.LastSeq = tx.LastSeq()
		return tx.Save(cp)
	})
	if errors.Is(err, store.ErrLockTimeout) {
		if perr := s.Park(ev); perr != nil {
			return nil, perr
		}
		return kernel.Decide(rows, ev, ctx), nil
	}
	if err != nil {
		return nil, err
	}
	return actions, nil
}
