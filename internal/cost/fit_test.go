package cost

import (
	"math"
	"math/rand"
	"testing"
)

func gridSamples(m Model) []Sample {
	var out []Sample
	for q := 64; q <= 4096; q *= 2 {
		for _, c := range []int{0, 512, 1024, 2048, 4096, 8192, 16384} {
			out = append(out, Sample{Q: q, C: c, MS: m.Cost(q, c)})
		}
	}
	return out
}

func TestFitExactRecovery(t *testing.T) {
	trueModel := Model{A: 6e-6, B: 5e-6, F: 0.04, G: 3.5}
	samples := gridSamples(trueModel)
	got, stats, err := Fit(samples)
	if err != nil {
		t.Fatal(err)
	}
	if stats.MAPE > 0.5 {
		t.Fatalf("MAPE = %f, want below 0.5%%", stats.MAPE)
	}
	if math.Abs(got.A-trueModel.A) > 0.05*trueModel.A {
		t.Fatalf("A = %g, want %g", got.A, trueModel.A)
	}
	if math.Abs(got.B-trueModel.B) > 0.05*trueModel.B {
		t.Fatalf("B = %g, want %g", got.B, trueModel.B)
	}
	if math.Abs(got.F-trueModel.F) > 0.02*trueModel.F {
		t.Fatalf("F = %g, want %g", got.F, trueModel.F)
	}
	if math.Abs(got.G-trueModel.G) > 0.05*trueModel.G {
		t.Fatalf("G = %g, want %g", got.G, trueModel.G)
	}
}

func TestFitWithNoise(t *testing.T) {
	trueModel := Model{A: 6e-6, B: 5e-6, F: 0.04, G: 3.5}
	rng := rand.New(rand.NewSource(7))
	samples := gridSamples(trueModel)
	for i := range samples {
		samples[i].MS *= 1 + 0.01*(rng.Float64()*2-1)
	}
	got, stats, err := Fit(samples)
	if err != nil {
		t.Fatal(err)
	}
	if stats.MAPE > 2 {
		t.Fatalf("MAPE = %f, want below 2%%", stats.MAPE)
	}
	if !got.Valid() {
		t.Fatalf("invalid model %+v", got)
	}
}

func TestFitRejectsBadInput(t *testing.T) {
	if _, _, err := Fit(nil); err == nil {
		t.Fatal("expected error for empty samples")
	}
	bad := []Sample{{Q: -1, C: 0, MS: 1}, {Q: 1, C: 0, MS: 1}, {Q: 2, C: 0, MS: 1}, {Q: 3, C: 0, MS: 1}}
	if _, _, err := Fit(bad); err == nil {
		t.Fatal("expected error for negative q")
	}
}

func TestCalibratorAdapts(t *testing.T) {
	trueModel := Model{A: 1.2e-5, B: 9e-6, F: 0.08, G: 4}
	base := Model{A: 2e-6, B: 2e-6, F: 0.02, G: 1}
	cal := NewCalibrator(base, 128, 4)
	rng := rand.New(rand.NewSource(11))
	for range 400 {
		q := 64 + rng.Intn(3000)
		c := (rng.Intn(6)) * 2048
		ms := trueModel.Cost(q, c) * (1 + 0.002*(rng.Float64()*2-1))
		cal.Observe(q, c, ms)
	}
	final := cal.Model()
	var sum, n float64
	for _, s := range gridSamples(trueModel) {
		sum += math.Abs(final.Cost(s.Q, s.C)-s.MS) / s.MS
		n++
	}
	mape := 100 * sum / n
	if mape > 5 {
		t.Fatalf("calibrated MAPE = %f, want below 5%% (model %+v)", mape, final)
	}
	if _, fits := cal.Stats(); fits == 0 {
		t.Fatal("expected at least one refit")
	}
}

func TestCalibratorIgnoresBadSamples(t *testing.T) {
	cal := NewCalibrator(DefaultModel(), 32, 4)
	before := cal.Model()
	cal.Observe(0, 0, 10)
	cal.Observe(10, 0, -1)
	after := cal.Model()
	if before != after {
		t.Fatal("invalid samples must not change the model")
	}
}
