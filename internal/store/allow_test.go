package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
)

func TestAllow_TokenIsTakenOnce(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "repo"), clock.Real())
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.TakeAllow("discard-uncommitted"); err != nil || ok {
		t.Fatalf("TakeAllow with no token = %v, %v; want false", ok, err)
	}
	if err := s.Allow("discard-uncommitted"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.TakeAllow("main-commit"); err != nil || ok {
		t.Errorf("TakeAllow of another row = %v, %v; want false", ok, err)
	}
	if ok, err := s.TakeAllow("discard-uncommitted"); err != nil || !ok {
		t.Errorf("first TakeAllow = %v, %v; want true", ok, err)
	}
	if ok, err := s.TakeAllow("discard-uncommitted"); err != nil || ok {
		t.Errorf("second TakeAllow = %v, %v; want false: a token is one-shot", ok, err)
	}
}

func TestAllow_RefusesAnIdThatIsNotARowName(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "repo"), clock.Real())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "../x", "a/b", `a\b`, "A", "x y", "."} {
		if err := s.Allow(id); !errors.Is(err, ErrBadRowID) {
			t.Errorf("Allow(%q) = %v, want ErrBadRowID", id, err)
		}
		if _, err := s.TakeAllow(id); !errors.Is(err, ErrBadRowID) {
			t.Errorf("TakeAllow(%q) = %v, want ErrBadRowID", id, err)
		}
	}
}

func TestAllow_ATokenExpiresAfterTenMinutes(t *testing.T) {
	s, dir, fc := open(t)
	if err := s.Allow("main-commit"); err != nil {
		t.Fatal(err)
	}
	fc.Advance(10 * time.Minute)
	if ok, err := s.TakeAllow("main-commit"); err != nil || !ok {
		t.Fatalf("a token exactly 10 minutes old: %v, %v; want it still valid", ok, err)
	}
	if err := s.Allow("main-commit"); err != nil {
		t.Fatal(err)
	}
	fc.Advance(10*time.Minute + time.Second)
	if ok, err := s.TakeAllow("main-commit"); err != nil || ok {
		t.Errorf("a token 10m1s old: %v, %v; want it ignored", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "allow", "main-commit")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the expired token file is still there: %v", err)
	}
}

func TestAllow_TakeAllowsIsAllOrNothing(t *testing.T) {
	s, _, _ := open(t)
	if err := s.Allow("main-commit"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.TakeAllows("main-commit", "hook-bypass"); err != nil || ok {
		t.Fatalf("TakeAllows with one token missing = %v, %v; want false", ok, err)
	}
	if ok, err := s.TakeAllow("main-commit"); err != nil || !ok {
		t.Fatalf("the present token was burnt by the failed TakeAllows (%v, %v)", ok, err)
	}
	for _, id := range []string{"main-commit", "hook-bypass"} {
		if err := s.Allow(id); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := s.TakeAllows("main-commit", "hook-bypass"); err != nil || !ok {
		t.Fatalf("TakeAllows with both tokens = %v, %v; want true", ok, err)
	}
	if ok, _ := s.TakeAllow("hook-bypass"); ok {
		t.Error("hook-bypass token survived TakeAllows")
	}
}
