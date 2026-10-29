package cost

import (
	"math"
	"testing"
)

func TestCostMonotonic(t *testing.T) {
	m := Model{A: 1e-6, B: 2e-6, F: 0.04, G: 3}
	if m.Cost(0, 0) != m.G {
		t.Fatalf("empty cost = %f, want %f", m.Cost(0, 0), m.G)
	}
	if m.Cost(100, 1000) <= m.Cost(100, 0) {
		t.Fatal("cost should increase with cached context")
	}
	if m.Cost(200, 1000) <= m.Cost(100, 1000) {
		t.Fatal("cost should increase with query length")
	}
	if !m.Valid() {
		t.Fatal("model should be valid")
	}
}

func TestSavingIdentityWhenCoefficientsEqual(t *testing.T) {
	m := Model{A: 5e-6, B: 5e-6, F: 0.04, G: 2.5}
	cases := []struct{ q, l int }{
		{1, 1}, {64, 512}, {200, 1600}, {1800, 8192}, {4096, 16384},
	}
	for _, tc := range cases {
		got := m.Saving(tc.q, tc.l)
		want := m.HitValue(tc.l)
		if math.Abs(got-want) > 1e-9*math.Max(1, want) {
			t.Errorf("q=%d l=%d saving=%f want hit value %f", tc.q, tc.l, got, want)
		}
	}
}

func TestSavingGeneralForm(t *testing.T) {
	m := Model{A: 3e-6, B: 7e-6, F: 0.05, G: 4}
	q, l := 300, 4000
	want := m.B*float64(l*(l+1))/2 + (m.B-m.A)*float64(q*l) + m.F*float64(l)
	got := m.Saving(q, l)
	if math.Abs(got-want) > 1e-6*math.Max(1, want) {
		t.Fatalf("saving = %f, want %f", got, want)
	}
}

func TestAttentionCrossover(t *testing.T) {
	m := Model{A: 6.7e-6, B: 6.7e-6, F: 0.05, G: 5}
	cross := 2 * m.F / m.B
	share := m.AttentionShare(int(cross), 0)
	if share < 0.45 || share > 0.55 {
		t.Fatalf("attention share at crossover = %f, want about 0.5", share)
	}
	if m.AttentionShare(1000, 0) >= 0.2 {
		t.Fatal("attention share should be small for short prompts")
	}
}

func TestDefaultModelValid(t *testing.T) {
	if !DefaultModel().Valid() {
		t.Fatal("default model must be valid")
	}
}
