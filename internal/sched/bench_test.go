package sched

import (
	"testing"

	"github.com/ab1nv/bales/internal/cost"
)

func BenchmarkECTPick(b *testing.B) {
	p := NewECT(cost.DefaultModel(), DefaultECTConfig())
	reps := make([]Replica, 8)
	for i := range reps {
		reps[i] = Replica{
			ID:           "r",
			InFlight:     i % 4,
			QueuedMS:     float64(i * 20),
			KVUsed:       i * 1000,
			KVMax:        65536,
			CachedTokens: (i % 3) * 1024,
			Speed:        1,
		}
	}
	req := Request{ID: "bench", QTokens: 4096, DecodeTokens: 64, Stream: true, SlackMS: 2000}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = p.Pick(req, reps)
	}
}
