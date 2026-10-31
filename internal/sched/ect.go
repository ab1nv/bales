package sched

import (
	"math"

	"github.com/ab1nv/bales/internal/cost"
)

// ECTConfig controls the earliest-completion-time scorer and its ITL guard.
type ECTConfig struct {
	ChunkTokens       int     // prefill chunk used for interference estimation
	ITL0MS            float64 // baseline decode step
	KappaMSPerToken   float64 // decode-step inflation per prefill token
	ITLBudgetMS       float64 // maximum tolerated predicted step
	ITLWeight         float64 // weight on step inflation in the score
	InterferenceMS    float64 // interference per active stream
	MaxStreamInFlight int     // guard against overpacked decode batches
}

// DefaultECTConfig returns conservative T4-class defaults.
func DefaultECTConfig() ECTConfig {
	return ECTConfig{
		ChunkTokens:       512,
		ITL0MS:            8,
		KappaMSPerToken:   0.02,
		ITLBudgetMS:       30,
		ITLWeight:         0.5,
		InterferenceMS:    2,
		MaxStreamInFlight: 32,
	}
}

// ECT is the Bales earliest-completion-time policy. All terms are milliseconds,
// so there are no unitless weights to tune for the main score.
type ECT struct {
	Model cost.Model
	Cfg   ECTConfig
}

// NewECT creates the policy.
func NewECT(model cost.Model, cfg ECTConfig) *ECT {
	return &ECT{Model: model, Cfg: cfg}
}

func (p *ECT) Name() string { return "bales_ect" }

func (p *ECT) Pick(req Request, reps []Replica) (int, string) {
	best := -1
	bestScore := math.Inf(1)
	bestReason := ""
	for i, r := range reps {
		if !healthy(r) {
			continue
		}
		q := max(req.QTokens, 0)
		cached := min(max(r.CachedTokens, 0), q)
		unc := q - cached
		if r.KVMax > 0 && r.KVUsed+unc+req.DecodeTokens > r.KVMax {
			continue
		}
		chunk := min(unc, p.Cfg.ChunkTokens)
		step := p.Cfg.ITL0MS + p.Cfg.KappaMSPerToken*float64(chunk)
		if req.Stream {
			if step > p.Cfg.ITLBudgetMS || r.InFlight >= p.Cfg.MaxStreamInFlight {
				continue
			}
		}
		wait := r.QueuedMS
		prefill := p.Model.Cost(unc, cached) / speed(r)
		interference := 0.0
		if req.Stream && r.InFlight > 0 {
			interference = p.Cfg.InterferenceMS*float64(r.InFlight) +
				p.Cfg.ITLWeight*math.Max(0, step-p.Cfg.ITL0MS)
		}
		ect := wait + prefill + interference
		if ect < bestScore {
			best, bestScore = i, ect
			bestReason = "lowest ECT"
		}
	}
	if best >= 0 {
		return best, bestReason
	}
	// Every replica violated a constraint. Relax them and take the least loaded
	// healthy replica so the request is not stranded.
	idx, _ := bestBy(reps, func(r Replica) float64 { return load(r) })
	return idx, "constraints relaxed"
}

// AffinityAction is the per-group placement choice.
type AffinityAction uint8

const (
	Spread AffinityAction = iota
	Pin
	Replicate
)

func (a AffinityAction) String() string {
	switch a {
	case Pin:
		return "pin"
	case Replicate:
		return "replicate"
	default:
		return "spread"
	}
}

// GateConfig thresholds the affinity decision. TauGate is the fraction of
// cluster capacity below which recoverable work is too small to justify skew.
type GateConfig struct {
	TauGate float64
	Theta   float64
}

// DefaultGateConfig returns the starting thresholds.
func DefaultGateConfig() GateConfig {
	return GateConfig{TauGate: 0.15, Theta: 0.8}
}

// GroupStat summarises one prefix group.
type GroupStat struct {
	RatePerSec       float64
	MeanSuffixTokens float64
	PrefixTokens     int
	Replicas         int
}

// Decide chooses spread, pin or replicate for a group. Capacity is expressed in
// milliseconds of prefill per second per replica, so the comparison with
// recoverable work is dimensional.
func (c GateConfig) Decide(stat GroupStat, model cost.Model, perReplicaCapacityMSps float64) (AffinityAction, int) {
	reps := max(stat.Replicas, 1)
	if perReplicaCapacityMSps <= 0 {
		perReplicaCapacityMSps = 1
	}
	tau := c.TauGate
	if tau <= 0 {
		tau = 0.15
	}
	theta := c.Theta
	if theta <= 0 {
		theta = 0.8
	}
	recoverable := stat.RatePerSec * model.Saving(int(stat.MeanSuffixTokens), stat.PrefixTokens)
	if recoverable < tau*perReplicaCapacityMSps*float64(reps) {
		return Spread, 0
	}
	k := int(math.Ceil(recoverable / (theta * perReplicaCapacityMSps)))
	k = min(max(k, 1), reps)
	if reps == 1 {
		return Pin, 1
	}
	if k <= 1 {
		return Pin, 1
	}
	return Replicate, k
}
