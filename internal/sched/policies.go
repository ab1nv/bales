package sched

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ab1nv/bales/internal/cost"
)

// RoundRobin rotates over healthy replicas.
type RoundRobin struct{ n atomic.Uint64 }

func (p *RoundRobin) Name() string { return "round_robin" }

func (p *RoundRobin) Pick(_ Request, reps []Replica) (int, string) {
	if len(reps) == 0 {
		return 0, "no replicas"
	}
	start := int(p.n.Add(1)-1) % len(reps)
	for k := range reps {
		i := (start + k) % len(reps)
		if healthy(reps[i]) {
			return i, "rotation"
		}
	}
	return 0, "no healthy replica"
}

// LeastConn picks the replica with the fewest in-flight requests.
type LeastConn struct{}

func (p *LeastConn) Name() string { return "least_conn" }

func (p *LeastConn) Pick(_ Request, reps []Replica) (int, string) {
	return bestBy(reps, func(r Replica) float64 {
		return float64(r.InFlight)*1e6 + r.QueuedMS
	})
}

// LeastTokens picks the replica that must compute the fewest new tokens.
type LeastTokens struct{}

func (p *LeastTokens) Name() string { return "least_tokens" }

func (p *LeastTokens) Pick(req Request, reps []Replica) (int, string) {
	return bestBy(reps, func(r Replica) float64 {
		return float64(uncached(req, r)+req.DecodeTokens) + 1e-3*r.QueuedMS
	})
}

// LLMStyle approximates the llm-d router: prefix match, queue length, KV
// utilisation and in-flight count combined with hand-tuned weights. The real
// router also uses speculative indexing, which the gateway's directory records
// at routing time.
type LLMStyle struct{}

func (p *LLMStyle) Name() string { return "llmd_style" }

func (p *LLMStyle) Pick(req Request, reps []Replica) (int, string) {
	return bestBy(reps, func(r Replica) float64 {
		return float64(uncached(req, r)) +
			0.5*r.QueuedMS +
			50*kvUtil(r) +
			2*float64(r.InFlight)
	})
}

// DynamoApprox is a cost-based router using a linear uncached-token estimate,
// matching the approximate mode of the Dynamo KV router.
type DynamoApprox struct {
	Model            cost.Model
	DecodeMSPerToken float64
	QueuedWeight     float64
}

func (p *DynamoApprox) Name() string { return "dynamo_approx" }

func (p *DynamoApprox) config() (float64, float64) {
	d := p.DecodeMSPerToken
	if d <= 0 {
		d = 2
	}
	q := p.QueuedWeight
	if q <= 0 {
		q = 1
	}
	return d, q
}

func (p *DynamoApprox) Pick(req Request, reps []Replica) (int, string) {
	decodeMS, queuedW := p.config()
	return bestBy(reps, func(r Replica) float64 {
		unc := uncached(req, r)
		return p.Model.LinearCost(unc)/speed(r) +
			float64(req.DecodeTokens)*decodeMS +
			queuedW*r.QueuedMS
	})
}

// DualMap is a two-hash power-of-two-choices router with a prefix bonus. It is
// an approximation of the published DualMap policy, not a full port.
type DualMap struct{ n atomic.Uint64 }

func (p *DualMap) Name() string { return "dualmap" }

func (p *DualMap) Pick(req Request, reps []Replica) (int, string) {
	n := len(reps)
	if n == 0 {
		return 0, "no replicas"
	}
	c := p.n.Add(1) - 1
	i := int(c % uint64(n))
	j := int((c*3 + 1) % uint64(n))
	if i == j {
		j = (i + 1) % n
	}
	si := mapScore(reps[i])
	sj := mapScore(reps[j])
	if sj < si {
		return j, "two-choice"
	}
	return i, "two-choice"
}

func mapScore(r Replica) float64 {
	if !healthy(r) {
		return math.Inf(1)
	}
	score := load(r)
	// A meaningful prefix advantage can outweigh a modest load difference.
	if r.CachedTokens >= 1024 {
		score -= float64(r.CachedTokens) * 0.05
	}
	return score
}

// Preble explores with probability 1/5 and otherwise exploits the best prefix
// match, a simplification of Preble's E2 policy.
type Preble struct{ n atomic.Uint64 }

func (p *Preble) Name() string { return "preble_e2" }

func (p *Preble) Pick(req Request, reps []Replica) (int, string) {
	c := p.n.Add(1)
	if c%5 == 0 {
		return bestBy(reps, func(r Replica) float64 { return load(r) })
	}
	best := -1
	bestCached := -1
	bestLoad := math.Inf(1)
	for i, r := range reps {
		if !healthy(r) {
			continue
		}
		if r.CachedTokens > bestCached ||
			(r.CachedTokens == bestCached && load(r) < bestLoad) {
			best, bestCached, bestLoad = i, r.CachedTokens, load(r)
		}
	}
	if best < 0 {
		return 0, "no healthy replica"
	}
	return best, "prefix match"
}

// CacheRoute pins chains to replicas for a TTL and reassigns under overload,
// following the planned-affinity approach in CacheRoute.
type CacheRoute struct {
	TTL time.Duration
	Now func() time.Time

	mu       sync.Mutex
	assigned map[uint64]assignment
}

type assignment struct {
	index   int
	expires time.Time
}

func (p *CacheRoute) Name() string { return "cacheroute_style" }

func (p *CacheRoute) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *CacheRoute) Pick(req Request, reps []Replica) (int, string) {
	if len(reps) == 0 {
		return 0, "no replicas"
	}
	ttl := p.TTL
	if ttl <= 0 {
		ttl = 2 * time.Second
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.assigned == nil {
		p.assigned = make(map[uint64]assignment)
	}
	if a, ok := p.assigned[req.Key]; ok && req.Key != 0 && now.Before(a.expires) {
		if a.index < len(reps) && healthy(reps[a.index]) &&
			reps[a.index].QueuedMS < 2000 && kvUtil(reps[a.index]) < 0.95 {
			return a.index, "pinned"
		}
	}
	idx, reason := bestBy(reps, func(r Replica) float64 {
		return float64(uncached(req, r)) + 0.2*r.QueuedMS
	})
	if req.Key != 0 {
		if len(p.assigned) > 10000 {
			p.assigned = make(map[uint64]assignment)
		}
		p.assigned[req.Key] = assignment{index: idx, expires: now.Add(ttl)}
	}
	return idx, reason
}

// BalesScore is the v1 weighted-sum policy, kept as an ablation.
type BalesScore struct {
	W1, W2, W3, W4 float64
}

func (p *BalesScore) Name() string { return "bales_score" }

func (p *BalesScore) weights() (float64, float64, float64, float64) {
	w1, w2, w3, w4 := p.W1, p.W2, p.W3, p.W4
	if w1 == 0 && w2 == 0 && w3 == 0 && w4 == 0 {
		w1, w2, w3, w4 = 1, 0.5, 50, 5
	}
	return w1, w2, w3, w4
}

func (p *BalesScore) Pick(req Request, reps []Replica) (int, string) {
	w1, w2, w3, w4 := p.weights()
	return bestBy(reps, func(r Replica) float64 {
		return w1*float64(uncached(req, r)) +
			w2*r.QueuedMS +
			w3*kvUtil(r) +
			w4*float64(r.InFlight)
	})
}
