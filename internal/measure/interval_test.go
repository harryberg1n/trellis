package measure

import "testing"

func TestBootstrap_ConstantSampleHasAPointInterval(t *testing.T) {
	lo, hi := Bootstrap([]float64{4, 4, 4, 4})
	if lo != 4 || hi != 4 {
		t.Errorf("interval = [%v, %v], want [4, 4]", lo, hi)
	}
}

func TestBootstrap_BoundedDeterministicAndSeeded(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 100}
	lo, hi := Bootstrap(xs)
	lo2, hi2 := Bootstrap(xs)
	if lo != lo2 || hi != hi2 {
		t.Errorf("two runs differ: [%v,%v] [%v,%v]", lo, hi, lo2, hi2)
	}
	if lo < 1 || hi > 100 || lo > 22 || hi < 22 {
		t.Errorf("interval [%v, %v] must lie in [1,100] around the mean 22", lo, hi)
	}
	if Seed != 1 || Resamples != 2000 {
		t.Errorf("seed %d resamples %d, want 1 and 2000", Seed, Resamples)
	}
}

func TestBootstrap_EmptyIsZero(t *testing.T) {
	if lo, hi := Bootstrap(nil); lo != 0 || hi != 0 {
		t.Errorf("interval = [%v, %v], want [0, 0]", lo, hi)
	}
}
