package clock

import (
	"sync"
	"time"
)

type waiter struct {
	at time.Time
	ch chan time.Time
}

// FakeClock is a clock a test moves by hand.
type FakeClock struct {
	mu      sync.Mutex
	cond    *sync.Cond
	now     time.Time
	waiters []waiter
}

// Fake returns a FakeClock reading start.
func Fake(start time.Time) *FakeClock {
	f := &FakeClock{now: start}
	f.cond = sync.NewCond(&f.mu)
	return f
}

func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After returns a channel that receives once the clock has advanced d.
func (f *FakeClock) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, waiter{at: f.now.Add(d), ch: ch})
	f.cond.Broadcast()
	return ch
}

// Advance moves the clock forward and fires every timer it reaches.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	kept := f.waiters[:0]
	for _, w := range f.waiters {
		if w.at.After(f.now) {
			kept = append(kept, w)
			continue
		}
		w.ch <- w.at
	}
	f.waiters = kept
	f.cond.Broadcast()
}

// BlockUntil returns once exactly n timers are pending. A test uses it to
// know that code under test is waiting on the clock before it advances it.
func (f *FakeClock) BlockUntil(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for len(f.waiters) != n {
		f.cond.Wait()
	}
}
