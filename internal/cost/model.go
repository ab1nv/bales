// Package cost implements the calibrated prefill cost model used by the Bales
// gateway, together with offline fitting and online refinement.
package cost

import "math"

// Model is a prefill cost model in milliseconds:
//
//	cost(q, c) = A*q*c + B*q*(q+1)/2 + F*q + G
//
// q is the number of uncached query tokens and c is the number of cached
// context tokens attended by those query tokens. A and B are kept separate so
// the fit can absorb kernel differences between the cached-context path and
// the in-chunk causal path.
type Model struct {
	A float64
	B float64
	F float64
	G float64
}

// Valid reports whether all coefficients are finite and non-negative.
func (m Model) Valid() bool {
	return isFinite(m.A) && isFinite(m.B) && isFinite(m.F) && isFinite(m.G) &&
		m.A >= 0 && m.B >= 0 && m.F >= 0 && m.G >= 0
}

// Cost returns the predicted prefill time in milliseconds.
func (m Model) Cost(q, c int) float64 {
	if q < 0 {
		q = 0
	}
	if c < 0 {
		c = 0
	}
	qf := float64(q)
	return m.A*qf*float64(c) + m.B*qf*float64(q+1)/2 + m.F*qf + m.G
}

// Work returns cost without the fixed overhead G.
func (m Model) Work(q, c int) float64 {
	return math.Max(0, m.Cost(q, c)-m.G)
}

// HitValue returns the prefill work of an L-token prefix, which is the value of
// a cache hit under the model when A equals B. See Saving for the general form.
func (m Model) HitValue(l int) float64 {
	if l <= 0 {
		return 0
	}
	return math.Max(0, m.Cost(l, 0)-m.G)
}

// Saving returns the exact work avoided when the first l tokens of a q-token
// query are cached: cost(q+l, 0) - cost(q, l). With A equal to B this equals
// HitValue(l); when they differ, the cross term (B-A)*q*l remains.
func (m Model) Saving(q, l int) float64 {
	if q <= 0 || l <= 0 {
		return 0
	}
	return math.Max(0, m.Cost(q+l, 0)-m.Cost(q, l))
}

// AttentionShare returns the fraction of prefill work attributed to attention
// terms for a query of q new tokens over c cached tokens.
func (m Model) AttentionShare(q, c int) float64 {
	work := m.Work(q, c)
	if work <= 0 {
		return 0
	}
	qf := float64(q)
	attn := m.A*qf*float64(max(c, 0)) + m.B*qf*float64(q+1)/2
	return math.Min(1, attn/work)
}

// LinearCost is the uncached-token baseline cost used by simple routers:
// F*q + G. It is reported alongside the full model in calibration output.
func (m Model) LinearCost(q int) float64 {
	if q < 0 {
		q = 0
	}
	return m.F*float64(q) + m.G
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
