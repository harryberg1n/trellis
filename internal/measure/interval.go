package measure

import (
	"math/rand/v2"
	"sort"
)

// Seed and Resamples fix the bootstrap, so a report is reproducible.
const (
	Seed      = 1
	Resamples = 2000
)

// MinIntervalSamples is the fewest samples an interval is printed for. An
// interval over less is not evidence: one sample has an interval of exactly
// itself.
//
// Adapted from aphrollo-tools internal/measure/abdecide.go:50-58 at 12b3724e.
// Incident: an all-zero or one-point sample printed a confident interval.
const MinIntervalSamples = 10

// intervalRanks are the 0-based positions of the 5th and 95th percentile
// (nearest rank) among n sorted draws: the ends of a 90% interval.
//
// Copied from aphrollo-tools internal/measure/abdecide.go:318 at 12b3724e.
// Incident: the 5th percentile of 2000 draws is the 100th, index 99, not 100.
func intervalRanks(n int) (lo, hi int) { return n/20 - 1, n*19/20 - 1 }

// Bootstrap is the 90% interval of the mean of xs: the 5th and 95th
// percentile of Resamples resampled means, drawn with a PCG seeded with Seed.
// An empty sample gives 0, 0.
func Bootstrap(xs []float64) (lo, hi float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	r := rand.New(rand.NewPCG(Seed, Seed))
	means := make([]float64, Resamples)
	for i := range means {
		var sum float64
		for range xs {
			sum += xs[r.IntN(len(xs))]
		}
		means[i] = sum / float64(len(xs))
	}
	sort.Float64s(means)
	l, h := intervalRanks(Resamples)
	return means[l], means[h]
}
