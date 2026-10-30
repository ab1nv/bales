// Package coalesce implements gateway-level in-flight prefix coalescing with a
// load-aware hold-or-spill rule and a bounded hold deadline.
package coalesce

import (
	"time"

	"github.com/ab1nv/bales/internal/cost"
)

// Action is the outcome of a coalescing decision.
type Action uint8

const (
	// Dispatch means no leader is in flight; route normally and become leader.
	Dispatch Action = iota
	// Hold means wait for the current leader, then route warm.
	Hold
	// Spill means route now to the best alternative replica.
	Spill
)

func (a Action) String() string {
	switch a {
	case Hold:
		return "hold"
	case Spill:
		return "spill"
	default:
		return "dispatch"
	}
}

// Config holds hold-or-spill thresholds. Units are milliseconds.
type Config struct {
	EpsilonMS      float64 // required saving before holding
	DeltaMaxMS     float64 // hard cap on hold time
	DefaultSlackMS float64 // slack used when the request does not set one
	Now            func() time.Time
}

// Coalescer decides whether a request should wait for an in-flight leader.
type Coalescer struct {
	reg   *Registry
	model cost.Model
	cfg   Config
	now   func() time.Time
}

// New creates a coalescer. Zero thresholds are replaced with conservative
// defaults: 1ms epsilon, 250ms hold cap, 2s slack.
func New(model cost.Model, cfg Config) *Coalescer {
	if cfg.EpsilonMS <= 0 {
		cfg.EpsilonMS = 1
	}
	if cfg.DeltaMaxMS <= 0 {
		cfg.DeltaMaxMS = 250
	}
	if cfg.DefaultSlackMS <= 0 {
		cfg.DefaultSlackMS = 2000
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Coalescer{reg: NewRegistry(), model: model, cfg: cfg, now: cfg.Now}
}

// Registry exposes the leader registry for integration and tests.
func (c *Coalescer) Registry() *Registry { return c.reg }

// Arrival describes a request that is considering coalescing.
type Arrival struct {
	Key          uint64
	QTokens      int
	SharedTokens int     // longest uncached shared prefix with the leader
	WaitLeaderMS float64 // queue wait on the leader's replica
	WaitBestMS   float64 // queue wait on the best alternative
	SlackMS      float64 // <=0 uses Config.DefaultSlackMS
}

// Decision is the outcome plus telemetry.
type Decision struct {
	Action   Action
	Leader   *Group
	Deadline time.Duration
	SavingMS float64
	Reason   string
}

// Decide evaluates the hold-or-spill rule.
//
//	T_hold  = tau + wait_leader + cost(q, L)
//	T_spill = wait_best + cost(q+L, 0)
//
// so holding wins when tau + wait_leader - wait_best < saving(q, L), where
// saving(q, L) = cost(q+L, 0) - cost(q, L). The hold deadline is capped by the
// request slack and by DeltaMax, which bounds the regret relative to spilling
// immediately.
func (c *Coalescer) Decide(a Arrival) Decision {
	g := c.reg.Get(a.Key)
	if g == nil {
		return Decision{Action: Dispatch, Reason: "no in-flight leader"}
	}
	slack := a.SlackMS
	if slack <= 0 {
		slack = c.cfg.DefaultSlackMS
	}
	saving := c.model.Saving(a.QTokens, a.SharedTokens)
	d := Decision{Action: Spill, Leader: g, SavingMS: saving}

	if a.QTokens <= 0 || a.SharedTokens <= 0 {
		d.Reason = "no shared prefix"
		return d
	}

	tau := g.EstDone.Sub(c.now()).Seconds() * 1000
	if tau < 0 {
		tau = 0
	}
	if a.SlackMS > 0 && tau > a.SlackMS {
		d.Reason = "leader exceeds slack"
		return d
	}
	if tau > c.cfg.DeltaMaxMS {
		d.Reason = "hold exceeds cap"
		return d
	}
	gain := saving - tau - (a.WaitLeaderMS - a.WaitBestMS)
	if gain <= c.cfg.EpsilonMS {
		d.Reason = "spill is cheaper"
		return d
	}

	holdMS := 1.5 * tau
	holdMS = min(holdMS, slack)
	holdMS = min(holdMS, c.cfg.DeltaMaxMS)
	if holdMS < 0 {
		holdMS = 0
	}
	d.Action = Hold
	d.Deadline = time.Duration(holdMS * float64(time.Millisecond))
	d.Reason = "hold saves prefill work"
	return d
}
