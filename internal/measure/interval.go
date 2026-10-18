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
	return means[Resamples*5/100], means[Resamples*95/100-1]
}
