package store

import "testing"

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
	defer f.Close()
	if ok, err := tryLock(f); ok || err != nil {
		t.Fatalf("tryLock while held = %v, %v; want false, nil", ok, err)
	}
	release()
	if ok, err := tryLock(f); !ok || err != nil {
		t.Fatalf("tryLock after release = %v, %v; want true, nil", ok, err)
	}
	unlock(f)
}
