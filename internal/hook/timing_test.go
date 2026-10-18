package hook

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/testiso"
	"github.com/harryberg1n/trellis/internal/testsupport"
)

var timingStart = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// stepClock moves forward by step on every Now, so a test sees which read
// stamped which field.
type stepClock struct {
	clock.Clock
	at   time.Time
	step time.Duration
}

func (c *stepClock) Now() time.Time {
	c.at = c.at.Add(c.step)
	return c.at
}

func editPayload(t *testing.T, e *env) []byte {
	t.Helper()
	f := filepath.Join(e.repo, "x.txt")
	testiso.Write(t, f, "x\n")
	return fill(readShape(t, "edit_01_full"), vars(e.repo, f, "x\n"))
}

func TestTiming_EditCarriesTheElapsedFromStartToAppend(t *testing.T) {
	e := newEnv(t)
	e.deps.Start = timingStart
	e.deps.Clock = clock.Fake(timingStart.Add(37 * time.Millisecond))
	e.do(t, "PostToolUse", editPayload(t, e))
	evs := e.events(t)
	if len(evs) != 1 || evs[0].TookMS != 37 {
		t.Fatalf("events %+v, want one edit with TookMS 37", evs)
	}
}

func TestTiming_IsReadJustBeforeTheAppend(t *testing.T) {
	e := newEnv(t)
	e.deps.Start = timingStart
	// Now is read once for At and once at the stamp: 10 ms, then 20 ms.
	e.deps.Clock = &stepClock{at: timingStart, step: 10 * time.Millisecond}
	e.do(t, "PostToolUse", editPayload(t, e))
	if evs := e.events(t); len(evs) != 1 || evs[0].TookMS != 20 {
		t.Fatalf("events %+v, want TookMS 20 (the second read)", evs)
	}
}

func TestTiming_StopCarriesIt(t *testing.T) {
	e := newEnv(t)
	e.deps.Start = timingStart
	e.deps.Clock = clock.Fake(timingStart.Add(180 * time.Millisecond))
	e.do(t, "Stop", fill(readShape(t, "stop"), vars(e.repo, "", "")))
	evs := e.events(t)
	if len(evs) != 1 || evs[0].Kind != "turn.anchored" || evs[0].TookMS != 180 {
		t.Fatalf("events %+v, want one turn.anchored with TookMS 180", evs)
	}
}

func TestTiming_AWallEventCarriesIt(t *testing.T) {
	e := newEnv(t)
	testiso.FakeTool(t, "gitleaks", 0, "")
	e.deps.Start = timingStart
	e.deps.Clock = clock.Fake(timingStart.Add(11 * time.Millisecond))
	e.pre(t, e.repo, "git commit -m wip", true)
	evs := e.events(t)
	if len(evs) != 1 || evs[0].Kind != "wall.denied" || evs[0].TookMS != 11 {
		t.Fatalf("events %+v, want one wall.denied with TookMS 11", evs)
	}
}

func TestTiming_ASubMillisecondRunIsOneNotZero(t *testing.T) {
	e := newEnv(t)
	e.deps.Start = timingStart
	e.deps.Clock = clock.Fake(timingStart.Add(300 * time.Microsecond))
	e.do(t, "PostToolUse", editPayload(t, e))
	if evs := e.events(t); len(evs) != 1 || evs[0].TookMS != 1 {
		t.Fatalf("events %+v, want TookMS 1", evs)
	}
}

func TestTiming_NoStartLeavesItUnmeasured(t *testing.T) {
	e := newEnv(t)
	e.deps.Clock = clock.Fake(timingStart.Add(37 * time.Millisecond))
	e.do(t, "PostToolUse", editPayload(t, e))
	if evs := e.events(t); len(evs) != 1 || evs[0].TookMS != 0 {
		t.Fatalf("events %+v, want TookMS 0 without a start", evs)
	}
}

func TestTiming_AnInternalErrorInTheLogCarriesIt(t *testing.T) {
	e := newEnv(t)
	e.deps.Start = timingStart
	e.deps.Clock = clock.Fake(timingStart.Add(5 * time.Millisecond))
	e.do(t, "PostToolUse", []byte("{not json"))
	evs := e.events(t)
	if len(evs) != 1 || evs[0].Kind != "internal.error" || evs[0].TookMS != 5 {
		t.Fatalf("events %+v, want one internal.error with TookMS 5", evs)
	}
}

func TestTiming_TheFallbackParkCarriesIt(t *testing.T) {
	e := newEnv(t)
	e.deps.Start = timingStart
	e.deps.Clock = clock.Fake(timingStart.Add(9 * time.Millisecond))
	e.deps.Store, e.deps.Err = nil, errors.New("no store")
	var out, errb bytes.Buffer
	if code := Run("PostToolUse", bytes.NewReader(editPayload(t, e)), &out, &errb, e.deps); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	evs := testsupport.PendingEvents(t, e.fallback)
	if len(evs) != 1 || evs[0].Kind != "internal.error" || evs[0].TookMS != 9 {
		t.Fatalf("parked %+v, want one internal.error with TookMS 9", evs)
	}
}
