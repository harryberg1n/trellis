// Package clock is the only source of time for store and engine.
package clock

import "time"

// Clock is the time source. Tests use Fake; nothing sleeps.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type real struct{}

func (real) Now() time.Time                         { return time.Now() }
func (real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Real is the wall clock.
func Real() Clock { return real{} }
