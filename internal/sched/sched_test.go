package sched

import (
	"testing"
	"time"

	"github.com/ab1nv/bales/internal/cost"
)

func TestFactory(t *testing.T) {
	for _, name := range Names() {
		p, err := New(name, cost.DefaultModel())
		if err != nil {
			t.Fatalf("New(%q): %v", name, err)
		}
		if p.Name() == "" {
			t.Fatalf("policy %q has empty name", name)
		}
	}
	if _, err := New("does_not_exist", cost.DefaultModel()); err == nil {
		t.Fatal("expected error for unknown policy")
	}
}

func TestRoundRobinSkipsUnhealthy(t *testing.T) {
	p := &RoundRobin{}
	reps := []Replica{{ID: "a"}, {ID: "b", Unhealthy: true}, {ID: "c"}}
	seen := map[int]int{}
	for range 6 {
		i, _ := p.Pick(Request{}, reps)
		if reps[i].Unhealthy {
			t.Fatalf("picked unhealthy replica %d", i)
		}
		seen[i]++
	}
	if seen[0] == 0 || seen[2] == 0 {
		t.Fatalf("rotation missed a healthy replica: %v", seen)
	}
}

func TestLeastConn(t *testing.T) {
	p := &LeastConn{}
	reps := []Replica{{InFlight: 5}, {InFlight: 0}, {InFlight: 2}}
	i, _ := p.Pick(Request{}, reps)
	if i != 1 {
		t.Fatalf("picked %d, want 1", i)
	}
}

func TestLeastTokensPrefersCache(t *testing.T) {
	p := &LeastTokens{}
	req := Request{QTokens: 4000, DecodeTokens: 32}
	reps := []Replica{
		{ID: "cold", CachedTokens: 0},
		{ID: "warm", CachedTokens: 3500},
	}
	i, _ := p.Pick(req, reps)
	if i != 1 {
		t.Fatalf("picked %d, want warm replica 1", i)
	}
}

func TestECTWarmPrefixWins(t *testing.T) {
	p := NewECT(cost.DefaultModel(), DefaultECTConfig())
	req := Request{ID: "r", QTokens: 4200, DecodeTokens: 64, Stream: true}
	reps := []Replica{
		{ID: "warm", CachedTokens: 4000, KVMax: 100000, KVUsed: 4000},
		{ID: "cold", CachedTokens: 0, KVMax: 100000},
	}
	i, reason := p.Pick(req, reps)
	if i != 0 {
		t.Fatalf("picked %d (%s), want warm replica 0", i, reason)
	}
}

func TestECTKVConstraint(t *testing.T) {
	p := NewECT(cost.DefaultModel(), DefaultECTConfig())
	req := Request{ID: "r", QTokens: 500, DecodeTokens: 64}
	reps := []Replica{
		{ID: "tight", CachedTokens: 400, KVMax: 1000, KVUsed: 950},
		{ID: "roomy", CachedTokens: 0, KVMax: 100000},
	}
	i, _ := p.Pick(req, reps)
	if i != 1 {
		t.Fatalf("picked %d, want replica 1 after KV rejection", i)
	}
}

func TestECTITLGuard(t *testing.T) {
	cfg := DefaultECTConfig()
	cfg.MaxStreamInFlight = 1
	p := NewECT(cost.DefaultModel(), cfg)
	req := Request{ID: "r", QTokens: 500, DecodeTokens: 64, Stream: true}
	reps := []Replica{
		{ID: "busy", InFlight: 1, QueuedMS: 0, KVMax: 100000},
		{ID: "free", InFlight: 0, QueuedMS: 50, KVMax: 100000},
	}
	i, _ := p.Pick(req, reps)
	if i != 1 {
		t.Fatalf("picked %d, want replica 1 under ITL guard", i)
	}
}

func TestECTConstraintFallback(t *testing.T) {
	cfg := DefaultECTConfig()
	cfg.KappaMSPerToken = 0.2 // every 512-token chunk exceeds the ITL budget
	p := NewECT(cost.DefaultModel(), cfg)
	req := Request{ID: "r", QTokens: 2000, DecodeTokens: 10, Stream: true}
	reps := []Replica{
		{ID: "a", QueuedMS: 100, KVMax: 100000},
		{ID: "b", QueuedMS: 10, KVMax: 100000},
	}
	i, reason := p.Pick(req, reps)
	if i != 1 || reason != "constraints relaxed" {
		t.Fatalf("picked %d (%s), want relaxed fallback to replica 1", i, reason)
	}
}

func TestDynamoApproxPrefersWarm(t *testing.T) {
	p, err := New("dynamo_approx", cost.DefaultModel())
	if err != nil {
		t.Fatal(err)
	}
	req := Request{QTokens: 3000, DecodeTokens: 20}
	reps := []Replica{
		{CachedTokens: 0, QueuedMS: 0},
		{CachedTokens: 2500, QueuedMS: 10},
	}
	i, _ := p.Pick(req, reps)
	if i != 1 {
		t.Fatalf("picked %d, want warm replica 1", i)
	}
}

func TestCacheRoutePins(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	p := &CacheRoute{TTL: time.Minute, Now: func() time.Time { return now }}
	req := Request{Key: 42, QTokens: 1000}
	reps := []Replica{
		{CachedTokens: 500, KVMax: 100000},
		{CachedTokens: 800, KVMax: 100000},
	}
	first, _ := p.Pick(req, reps)
	second, reason := p.Pick(req, reps)
	if first != second || reason != "pinned" {
		t.Fatalf("first=%d second=%d reason=%s, want pinned", first, second, reason)
	}
}

func TestAffinityGate(t *testing.T) {
	model := cost.DefaultModel()
	cfg := DefaultGateConfig()
	const perReplica = 1000.0
	saving := model.Saving(200, 4096)
	cases := []struct {
		name  string
		stat  GroupStat
		want  AffinityAction
		count int
	}{
		{
			name: "low recoverable work spreads",
			stat: GroupStat{RatePerSec: 1, MeanSuffixTokens: 200, PrefixTokens: 4096, Replicas: 4},
			want: Spread,
		},
		{
			name: "moderate demand pins",
			stat: GroupStat{RatePerSec: 3, MeanSuffixTokens: 200, PrefixTokens: 4096, Replicas: 4},
			want: Pin, count: 1,
		},
		{
			name: "high demand replicates",
			stat: GroupStat{RatePerSec: 10, MeanSuffixTokens: 200, PrefixTokens: 4096, Replicas: 4},
			want: Replicate, count: 4,
		},
		{
			name: "single replica pins",
			stat: GroupStat{RatePerSec: 100, MeanSuffixTokens: 200, PrefixTokens: 4096, Replicas: 1},
			want: Pin, count: 1,
		},
		{
			name: "no prefix spreads",
			stat: GroupStat{RatePerSec: 100, MeanSuffixTokens: 200, PrefixTokens: 0, Replicas: 4},
			want: Spread,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			act, count := cfg.Decide(tc.stat, model, perReplica)
			if act != tc.want {
				t.Fatalf("action = %v, want %v (saving %f)", act, tc.want, saving)
			}
			if tc.want == Spread && count != 0 {
				t.Fatalf("spread count = %d, want 0", count)
			}
			if tc.count > 0 && count != tc.count {
				t.Fatalf("count = %d, want %d", count, tc.count)
			}
		})
	}
}
