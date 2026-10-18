package store

import (
	"errors"
	"testing"
	"time"
)

func TestLock_SecondHandleIsRefusedWhileHeld(t *testing.T) {
	s, _, _ := open(t)
	release, err := s.Hold()
	if err != nil {
		t.Fatal(err)
	}
	f, err := openLockFile(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if ok, err := tryLock(f); ok || err != nil {
		t.Fatalf("tryLock while held = %v, %v; want false, nil", ok, err)
	}
	release()
	if ok, err := tryLock(f); !ok || err != nil {
		t.Fatalf("tryLock after release = %v, %v; want true, nil", ok, err)
	}
	unlock(f)
}

func TestOpen_WithLockTimeoutSetsTheDeadline(t *testing.T) {
	s, dir, _ := open(t)
	release, err := s.Hold()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	clk := &spinClock{now: t0}
	other, err := Open(dir, clk, WithLockTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	err = other.Locked(func(*Tx) error { return nil })

	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("Locked = %v, want ErrLockTimeout", err)
	}
	if waited := clk.now.Sub(t0); waited < 50*time.Millisecond || waited > 60*time.Millisecond {
		t.Errorf("gave up after %v of store time, want about 50ms (the default is %v)", waited, LockTimeout)
	}
}
