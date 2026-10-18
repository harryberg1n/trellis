package store

import (
	"errors"
	"path/filepath"
	"testing"

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
