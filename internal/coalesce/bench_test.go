package coalesce

import (
	"testing"
	"time"
)

func BenchmarkDecide(b *testing.B) {
	base := time.Unix(1_700_000_000, 0)
	c := New(testModel(), Config{
		EpsilonMS:      1,
		DeltaMaxMS:     250,
		DefaultSlackMS: 2000,
		Now:            func() time.Time { return base },
	})
	c.reg.Start(1, 0, base, 60*time.Millisecond)
	a := Arrival{Key: 1, QTokens: 0, SharedTokens: 4096, SlackMS: 2000}
	b.ReportAllocs()
	for b.Loop() {
		_ = c.Decide(a)
	}
}
