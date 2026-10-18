package clock

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func TestFake_NowStartsAtStartAndAdvances(t *testing.T) {
	f := Fake(t0)
	if !f.Now().Equal(t0) {
		t.Fatalf("Now = %v, want %v", f.Now(), t0)
	}
	f.Advance(90 * time.Second)
	want := time.Date(2026, 10, 18, 12, 1, 30, 0, time.UTC)
	if !f.Now().Equal(want) {
		t.Fatalf("Now = %v, want %v", f.Now(), want)
	}
}

func TestFake_AfterFiresOnlyWhenAdvancedPastIt(t *testing.T) {
	f := Fake(t0)
	ch := f.After(2 * time.Second)
	f.Advance(time.Second)
	select {
	case <-ch:
		t.Fatal("fired after 1s of a 2s timer")
	default:
	}
	f.Advance(time.Second)
	select {
	case got := <-ch:
		if !got.Equal(t0.Add(2 * time.Second)) {
			t.Fatalf("fired with %v", got)
		}
	default:
		t.Fatal("did not fire after 2s")
	}
}

func TestFake_AfterZeroFiresAtOnce(t *testing.T) {
	f := Fake(t0)
	select {
	case <-f.After(0):
	default:
		t.Fatal("After(0) did not fire")
	}
}

func TestFake_BlockUntilSeesARegisteredWaiter(_ *testing.T) {
	f := Fake(t0)
	go f.After(time.Hour)
	f.BlockUntil(1)
	f.Advance(time.Hour)
	f.BlockUntil(0)
}

func TestReal_NowIsCloseToWallTime(t *testing.T) {
	if d := time.Since(Real().Now()); d < 0 || d > time.Minute {
		t.Fatalf("Real().Now() is %v behind the wall clock", d)
	}
}
