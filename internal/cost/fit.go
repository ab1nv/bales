package cost

import (
	"fmt"
	"math"
)

// Sample is one measured prefill observation from an idle replica.
type Sample struct {
	Q  int
	C  int
	MS float64
}

// FitStats summarises goodness of fit on the samples used for fitting.
type FitStats struct {
	Samples int
	RMSE    float64
	MAPE    float64
	R2      float64
}

// Fit solves a non-negative least squares problem over the four model terms.
// It uses column normalisation and cyclic coordinate descent, which is fast for
// four parameters and stable when the feature scales differ by orders of
// magnitude (q*c is roughly 10^7 for long contexts while the intercept is 1).
func Fit(samples []Sample) (Model, FitStats, error) {
	if len(samples) < 4 {
		return Model{}, FitStats{}, fmt.Errorf("cost: need at least 4 samples, got %d", len(samples))
	}

	const nFeatures = 4
	cols := make([][]float64, nFeatures)
	for j := range cols {
		cols[j] = make([]float64, len(samples))
	}
	y := make([]float64, len(samples))
	for i, s := range samples {
		if s.Q < 0 || s.C < 0 || s.MS < 0 || !isFinite(s.MS) {
			return Model{}, FitStats{}, fmt.Errorf("cost: invalid sample %d: %+v", i, s)
		}
		q := float64(s.Q)
		cols[0][i] = q * float64(s.C)
		cols[1][i] = q * (q + 1) / 2
		cols[2][i] = q
		cols[3][i] = 1
		y[i] = s.MS
	}

	var scale [nFeatures]float64
	x := make([][]float64, nFeatures)
	for j := range cols {
		var norm2 float64
		for _, v := range cols[j] {
			norm2 += v * v
		}
		scale[j] = math.Sqrt(norm2)
		if scale[j] == 0 || !isFinite(scale[j]) {
			scale[j] = 1
		}
		x[j] = make([]float64, len(y))
		for i, v := range cols[j] {
			x[j][i] = v / scale[j]
		}
	}

	var z [nFeatures]float64
	residual := make([]float64, len(y))
	copy(residual, y)
	maxIter := 50000
	tol := 1e-11
	for range maxIter {
		maxDelta := 0.0
		for j := range nFeatures {
			var dot float64
			for i := range residual {
				dot += x[j][i] * residual[i]
			}
			next := math.Max(0, z[j]+dot)
			delta := next - z[j]
			if delta == 0 {
				continue
			}
			z[j] = next
			for i := range residual {
				residual[i] -= delta * x[j][i]
			}
			if a := math.Abs(delta); a > maxDelta {
				maxDelta = a
			}
		}
		if maxDelta < tol {
			break
		}
	}

	m := Model{
		A: z[0] / scale[0],
		B: z[1] / scale[1],
		F: z[2] / scale[2],
		G: z[3] / scale[3],
	}
	if !m.Valid() {
		return Model{}, FitStats{}, fmt.Errorf("cost: fit produced invalid model %+v", m)
	}
	return m, evaluate(m, samples), nil
}

func evaluate(m Model, samples []Sample) FitStats {
	var sse, sst, mapeSum float64
	var mean float64
	for _, s := range samples {
		mean += s.MS
	}
	mean /= float64(len(samples))
	for _, s := range samples {
		pred := m.Cost(s.Q, s.C)
		diff := pred - s.MS
		sse += diff * diff
		d := s.MS - mean
		sst += d * d
		denom := math.Max(math.Abs(s.MS), 1e-12)
		mapeSum += math.Abs(diff) / denom
	}
	stats := FitStats{
		Samples: len(samples),
		RMSE:    math.Sqrt(sse / float64(len(samples))),
		MAPE:    100 * mapeSum / float64(len(samples)),
	}
	switch {
	case sst == 0 && sse == 0:
		stats.R2 = 1
	case sst == 0:
		stats.R2 = 0
	default:
		stats.R2 = 1 - sse/sst
	}
	return stats
}
