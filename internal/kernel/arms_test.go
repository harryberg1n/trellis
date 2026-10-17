package kernel

import (
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

func treatmentCount(experiment string, share, lanes int) int {
	n := 0
	for i := 0; i < lanes; i++ {
		if Assign(fmt.Sprintf("lane/%d", i), experiment, share) == "treatment" {
			n++
		}
	}
	return n
}

func TestAssign_KnownVector(t *testing.T) {
	// sha256("exp1\x00lane/x") = 24693450c8a5ea4d...; first 8 bytes BE mod 100 = 29.
	if got := Assign("lane/x", "exp1", 50); got != "treatment" {
		t.Errorf("Assign = %q, want treatment (bucket 29 < 50)", got)
	}
	if got := Assign("lane/x", "exp1", 29); got != "control" {
		t.Errorf("Assign at share 29 = %q, want control (29 is not < 29)", got)
	}
	if got := Assign("lane/x", "exp1", 30); got != "treatment" {
		t.Errorf("Assign at share 30 = %q, want treatment", got)
	}
}

func TestAssign_ShareWithinTwoPoints(t *testing.T) {
	// Property: a bigger share never moves a lane from treatment to control.
	rapid.Check(t, func(rt *rapid.T) {
		lane := rapid.StringMatching(`lane/[a-z0-9]{1,8}`).Draw(rt, "lane")
		lo := rapid.IntRange(0, 100).Draw(rt, "lo")
		hi := rapid.IntRange(lo, 100).Draw(rt, "hi")
		if Assign(lane, "exp", lo) == "treatment" && Assign(lane, "exp", hi) != "treatment" {
			rt.Fatalf("%s: treatment at %d but not at %d", lane, lo, hi)
		}
	})
	if n := treatmentCount("exp", 30, 10000); n < 2800 || n > 3200 {
		t.Errorf("treatment = %d of 10000 for lane/<n>, want 2800..3200", n)
	}
}

func TestAssign_ExperimentsAreIndependent(t *testing.T) {
	exps := []string{"a", "b", "c"}
	for i := 0; i < len(exps); i++ {
		for j := i + 1; j < len(exps); j++ {
			both := 0
			for n := 0; n < 10000; n++ {
				lane := fmt.Sprintf("lane/%d", n)
				if Assign(lane, exps[i], 50) == "treatment" && Assign(lane, exps[j], 50) == "treatment" {
					both++
				}
			}
			if both < 2300 || both > 2700 {
				t.Errorf("%s&%s co-treatment = %d of 10000, want 2300..2700", exps[i], exps[j], both)
			}
		}
	}
}

func TestAssign_EdgeShares(t *testing.T) {
	if Assign("lane/x", "exp1", 0) != "control" || Assign("lane/x", "exp1", 100) != "treatment" {
		t.Error("share 0 must be all control and share 100 all treatment")
	}
}

func TestSticky_ShareChangeDoesNotMoveALane(t *testing.T) {
	s := NewSticky()
	// lane/x hashes to bucket 29 under exp1: share 20 puts it in control.
	first := s.Resolve("lane/x", map[string]int{"exp1": 20})
	if first["exp1"] != "control" {
		t.Fatalf("first arm = %v, want exp1=control", first)
	}
	later := s.Resolve("lane/x", map[string]int{"exp1": 100})
	if later["exp1"] != "control" {
		t.Errorf("after share change arm = %v, want exp1=control", later)
	}
	other := s.Resolve("lane/y", map[string]int{"exp1": 100})
	if other["exp1"] != "treatment" {
		t.Errorf("a new lane at share 100 = %v, want treatment", other)
	}
}

func TestSticky_ObserveKeepsTheVectorOfTheFirstEvent(t *testing.T) {
	s := NewSticky()
	s.Observe(Event{Lane: "lane/x", Arms: map[string]string{"exp1": "control"}})
	s.Observe(Event{Lane: "lane/x", Arms: map[string]string{"exp1": "treatment"}})
	got := s.Resolve("lane/x", map[string]int{"exp1": 100})
	if got["exp1"] != "control" {
		t.Errorf("arms = %v, want exp1=control", got)
	}
}

func TestSticky_LateExperimentJoinsAtNextEventAndStays(t *testing.T) {
	s := NewSticky()
	s.Observe(Event{Lane: "lane/x", Arms: map[string]string{"a": "control"}})
	// lane/x is bucket 29 under exp1: share 50 gives treatment, share 20 control.
	got := s.Resolve("lane/x", map[string]int{"a": 100, "exp1": 50})
	if got["a"] != "control" || got["exp1"] != "treatment" {
		t.Fatalf("arms = %v, want a=control exp1=treatment", got)
	}
	later := s.Resolve("lane/x", map[string]int{"a": 100, "exp1": 20})
	if later["exp1"] != "treatment" {
		t.Errorf("late experiment moved: %v, want exp1=treatment", later)
	}
}

func TestSticky_FirstEventWithNoArmsDoesNotFreezeEmpty(t *testing.T) {
	s := NewSticky()
	s.Observe(Event{Lane: "lane/x"})
	if got := s.Resolve("lane/x", map[string]int{"exp1": 50}); got["exp1"] != "treatment" {
		t.Errorf("arms = %v, want exp1=treatment", got)
	}
}

func TestSticky_RecordedArmIsNeverOverwritten(t *testing.T) {
	s := NewSticky()
	s.Observe(Event{Lane: "lane/x", Arms: map[string]string{"exp1": "control"}})
	s.Observe(Event{Lane: "lane/x", Arms: map[string]string{"exp1": "treatment", "b": "treatment"}})
	got := s.Resolve("lane/x", map[string]int{"exp1": 100})
	if got["exp1"] != "control" || got["b"] != "treatment" {
		t.Errorf("arms = %v, want exp1=control b=treatment", got)
	}
}
