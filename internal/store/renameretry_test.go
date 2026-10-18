package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
)

var errBusy = errors.New("busy")

func anyErr(error) bool { return true }

// stepClock moves itself: each After is a wait that has already passed.
type stepClock struct{ now time.Time }

func (c *stepClock) Now() time.Time { return c.now }
func (c *stepClock) After(d time.Duration) <-chan time.Time {
	c.now = c.now.Add(d)
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

func TestRetryRename_RefusedTwiceThenSucceedsOnTheFakeClock(t *testing.T) {
	fc := clock.Fake(time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC))
	calls := 0
	rename := func() error {
		calls++
		if calls <= 2 {
			return errBusy
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- retryRename(fc, rename, anyErr, time.Second) }()

	fc.BlockUntil(1)
	fc.Advance(time.Millisecond)
	fc.BlockUntil(1)
	fc.Advance(2 * time.Millisecond)
	if err := <-done; err != nil || calls != 3 {
		t.Fatalf("err = %v after %d calls, want nil after 3", err, calls)
	}
}

func TestRetryRename_ReturnsAnErrorNothingWillClearAtOnce(t *testing.T) {
	calls := 0
	err := retryRename(&stepClock{}, func() error { calls++; return errBusy }, func(error) bool { return false }, time.Second)
	if !errors.Is(err, errBusy) || calls != 1 {
		t.Fatalf("err = %v after %d calls, want busy after 1", err, calls)
	}
}

func TestRetryRename_GivesUpAtTheBoundWithTheLastError(t *testing.T) {
	start := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	c := &stepClock{now: start}
	calls := 0
	err := retryRename(c, func() error { calls++; return errBusy }, anyErr, time.Second)
	if !errors.Is(err, errBusy) {
		t.Fatalf("err = %v, want the last error", err)
	}
	// Pauses of 1, 2, 4, 8, 16 ms, then 20 ms each: the 1 s bound is first
	// reached after 54 pauses (31 + 49*20 = 1011 ms), on the 55th try.
	if calls != 55 {
		t.Errorf("%d calls, want 55", calls)
	}
	if got := c.now.Sub(start); got != 1011*time.Millisecond {
		t.Errorf("gave up after %v of clock time, want 1.011s", got)
	}
}

func TestWriteAtomic_RetriesARenameTheOSRefusesAndPublishes(t *testing.T) {
	dir := t.TempDir()
	fc := clock.Fake(time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC))
	calls := 0
	rename := func(oldpath, newpath string) error {
		calls++
		if calls <= 2 {
			return errBusy
		}
		return os.Rename(oldpath, newpath)
	}
	done := make(chan error, 1)
	go func() {
		done <- WriteAtomic(dir, "s", []byte("new"), WithClock(fc), WithRename(rename), withRetryable(anyErr))
	}()
	fc.BlockUntil(1)
	fc.Advance(time.Millisecond)
	fc.BlockUntil(1)
	fc.Advance(2 * time.Millisecond)
	if err := <-done; err != nil || calls != 3 {
		t.Fatalf("err = %v after %d renames, want nil after 3", err, calls)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "s")); string(got) != "new" {
		t.Errorf("s = %q, want new", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d entries, want only s", len(entries))
	}
}
