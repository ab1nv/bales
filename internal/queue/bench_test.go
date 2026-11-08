package queue

import (
	"testing"
	"time"
)

func BenchmarkPushPop(b *testing.B) {
	base := time.Unix(1_700_000_000, 0)
	q := New(Config{DStream: 300 * time.Millisecond, DBatch: 5 * time.Second, Eta: 1, QMaxMS: 1e9})
	b.ReportAllocs()
	for b.Loop() {
		_, _ = q.Push(Request{ID: "bench", Arrival: base, EstCostMS: 10})
		q.Pop()
	}
}
