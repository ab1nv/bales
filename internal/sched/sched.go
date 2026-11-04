// Package sched contains replica selection policies. Every policy returns a
// millisecond-valued score or a documented heuristic so the simulator and the
// gateway share one implementation.
package sched

import (
	"fmt"
	"math"

	"github.com/ab1nv/bales/internal/cost"
)

// Request is the routing view of an inference request.
type Request struct {
	ID           string
	Key          uint64 // chain hash, used by affinity policies
	QTokens      int    // total prompt tokens
	DecodeTokens int    // predicted output tokens
	Stream       bool
	SlackMS      float64
}

// Replica is a point-in-time view of one backend.
type Replica struct {
	ID           string
	InFlight     int
	QueuedMS     float64 // estimated queued prefill work in milliseconds
	KVUsed       int     // tokens of KV currently occupied
	KVMax        int     // tokens of KV capacity, zero means unknown
	CachedTokens int     // believed cache hit for this request
	Speed        float64 // relative prefill speed, 1 is nominal
	Unhealthy    bool
}

// Policy selects a replica index for a request.
type Policy interface {
	Name() string
	Pick(req Request, reps []Replica) (index int, reason string)
}

// New builds a policy by name. The model is used by cost-aware policies.
func New(name string, model cost.Model) (Policy, error) {
	switch name {
	case "round_robin":
		return &RoundRobin{}, nil
	case "least_conn":
		return &LeastConn{}, nil
	case "least_tokens":
		return &LeastTokens{}, nil
	case "llmd_style":
		return &LLMStyle{}, nil
	case "dynamo_approx":
		return &DynamoApprox{Model: model}, nil
	case "dualmap":
		return &DualMap{}, nil
	case "preble_e2":
		return &Preble{}, nil
	case "cacheroute_style":
		return &CacheRoute{TTL: 2_000_000_000}, nil
	case "bales_score":
		return &BalesScore{}, nil
	case "bales_ect":
		return NewECT(model, DefaultECTConfig()), nil
	case "bales_ect_linear":
		ect := NewECT(model, DefaultECTConfig())
		ect.UseLinear = true
		return ect, nil
	default:
		return nil, fmt.Errorf("sched: unknown policy %q", name)
	}
}

// Names lists the implemented policies.
func Names() []string {
	return []string{
		"round_robin", "least_conn", "least_tokens", "llmd_style",
		"dynamo_approx", "dualmap", "preble_e2", "cacheroute_style",
		"bales_score", "bales_ect", "bales_ect_linear",
	}
}

func healthy(r Replica) bool { return !r.Unhealthy }

func speed(r Replica) float64 {
	if r.Speed <= 0 {
		return 1
	}
	return r.Speed
}

func uncached(req Request, r Replica) int {
	c := min(max(r.CachedTokens, 0), req.QTokens)
	return req.QTokens - c
}

func kvUtil(r Replica) float64 {
	if r.KVMax <= 0 {
		return 0
	}
	return math.Min(1, float64(r.KVUsed)/float64(r.KVMax))
}

func load(r Replica) float64 {
	return r.QueuedMS + float64(r.InFlight)*2
}

func bestBy(reps []Replica, score func(Replica) float64) (int, string) {
	best := -1
	bestScore := math.Inf(1)
	for i, r := range reps {
		if !healthy(r) {
			continue
		}
		s := score(r)
		if s < bestScore {
			best, bestScore = i, s
		}
	}
	if best < 0 {
		for i := range reps {
			if healthy(reps[i]) {
				return i, "fallback"
			}
		}
		return 0, "no healthy replica"
	}
	return best, ""
}
