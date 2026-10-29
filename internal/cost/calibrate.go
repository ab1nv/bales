package cost

import "sync"

// Calibrator refines a base model from recent idle-replica samples. The
// prototype uses a sliding-window NNLS refit instead of recursive least
// squares: it stays stable when the four feature columns are ill-conditioned,
// keeps the non-negativity constraint, and is cheap at gateway rates when the
// refit interval is a handful of samples.
type Calibrator struct {
	mu         sync.Mutex
	base       Model
	window     int
	refitEvery int
	model      Model
	samples    []Sample
	sinceFit   int
	fits       int
	stats      FitStats
}

// NewCalibrator returns a calibrator. window caps the retained samples and
// refitEvery controls how many new samples trigger a refit. Values below the
// minimums are raised to 32 and 4.
func NewCalibrator(base Model, window, refitEvery int) *Calibrator {
	if !base.Valid() {
		base = Model{F: 1, G: 1}
	}
	if window < 32 {
		window = 32
	}
	if refitEvery < 4 {
		refitEvery = 4
	}
	return &Calibrator{base: base, model: base, window: window, refitEvery: refitEvery}
}

// Observe feeds one clean measurement. Samples with non-positive q or ms are
// ignored. It returns the model currently in use.
func (c *Calibrator) Observe(q, cTokens int, ms float64) Model {
	if q <= 0 || ms <= 0 || !isFinite(ms) || cTokens < 0 {
		return c.Model()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.samples = append(c.samples, Sample{Q: q, C: cTokens, MS: ms})
	if len(c.samples) > c.window {
		c.samples = c.samples[len(c.samples)-c.window:]
	}
	c.sinceFit++
	if c.sinceFit >= c.refitEvery && len(c.samples) >= 8 {
		if m, st, err := Fit(c.samples); err == nil {
			c.model = blend(c.model, m, 0.5)
			c.stats = st
			c.fits++
		}
		c.sinceFit = 0
	}
	return c.model
}

// Model returns a copy of the current model.
func (c *Calibrator) Model() Model {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.model
}

// Stats returns the most recent fit statistics and the number of refits.
func (c *Calibrator) Stats() (FitStats, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats, c.fits
}

// blend damps a refit toward the previous model so a noisy window cannot swing
// routing decisions abruptly. The vector is renormalised only by component-wise
// interpolation, which keeps non-negativity.
func blend(prev, next Model, alpha float64) Model {
	return Model{
		A: prev.A*(1-alpha) + next.A*alpha,
		B: prev.B*(1-alpha) + next.B*alpha,
		F: prev.F*(1-alpha) + next.F*alpha,
		G: prev.G*(1-alpha) + next.G*alpha,
	}
}

// DefaultModel is a rough Qwen2.5-1.5B class profile on a T4, used before the
// calibration sweep runs. Attention and linear coefficients are chosen so the
// attention crossover sits near 15k context tokens, matching the FLOP analysis
// in the plan.
func DefaultModel() Model {
	return Model{
		A: 6.7e-6,
		B: 6.7e-6,
		F: 0.05,
		G: 5,
	}
}
