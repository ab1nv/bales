// Package sim is a trace-driven, single-process simulator for routing and
// coalescing experiments. Replicas process one prefill and decode at a time;
// this overstates queueing relative to continuous batching, so it is used for
// relative comparisons only.
package sim

import (
	"container/heap"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/ab1nv/bales/internal/coalesce"
	"github.com/ab1nv/bales/internal/cost"
	"github.com/ab1nv/bales/internal/prefix"
	"github.com/ab1nv/bales/internal/sched"
)

// Config controls one simulated cluster.
type Config struct {
	Replicas       int
	PolicyName     string
	Coalesce       bool
	Cost           cost.Model
	DecodeMS       float64
	CacheBlocks    int
	BlockTokens    int
	KVMax          int
	DeltaMaxMS     float64
	EpsilonMS      float64
	DefaultSlackMS float64
	SLOTargetMS    float64
	Speculative    bool
}

// DefaultConfig returns a four-replica setup.
func DefaultConfig() Config {
	return Config{
		Replicas:       4,
		PolicyName:     "bales_ect",
		Cost:           cost.DefaultModel(),
		DecodeMS:       2,
		CacheBlocks:    2048,
		BlockTokens:    512,
		KVMax:          65536,
		DeltaMaxMS:     250,
		EpsilonMS:      1,
		DefaultSlackMS: 2000,
		SLOTargetMS:    2000,
		Speculative:    true,
	}
}

func (c Config) normalized() Config {
	d := DefaultConfig()
	if c.Replicas <= 0 {
		c.Replicas = d.Replicas
	}
	if c.PolicyName == "" {
		c.PolicyName = d.PolicyName
	}
	if !c.Cost.Valid() {
		c.Cost = d.Cost
	}
	if c.DecodeMS <= 0 {
		c.DecodeMS = d.DecodeMS
	}
	if c.CacheBlocks <= 0 {
		c.CacheBlocks = d.CacheBlocks
	}
	if c.BlockTokens <= 0 {
		c.BlockTokens = d.BlockTokens
	}
	if c.KVMax <= 0 {
		c.KVMax = d.KVMax
	}
	if c.DeltaMaxMS <= 0 {
		c.DeltaMaxMS = d.DeltaMaxMS
	}
	if c.EpsilonMS <= 0 {
		c.EpsilonMS = d.EpsilonMS
	}
	if c.DefaultSlackMS <= 0 {
		c.DefaultSlackMS = d.DefaultSlackMS
	}
	if c.SLOTargetMS <= 0 {
		c.SLOTargetMS = d.SLOTargetMS
	}
	return c
}

// Request is one trace entry.
type Request struct {
	ID           string
	ArrivalMS    float64
	PromptTokens int
	OutputTokens int
	Group        string
	Chain        []uint64
	Stream       bool
}

// Trace is a request stream.
type Trace struct {
	Name       string
	DurationMS float64
	Requests   []Request
}

// Result is the per-request simulation output.
type Result struct {
	ID           string
	Group        string
	PromptTokens int
	OutputTokens int
	Stream       bool
	Replica      int
	Follower     bool
	Coalesced    bool
	Hold         bool
	HoldMS       float64
	HoldOutcome  string
	CachedTokens int
	ArrivalMS    float64
	DispatchMS   float64
	StartMS      float64
	PrefillMS    float64
	QueueWaitMS  float64
	TTFTMS       float64
	DoneMS       float64
	Policy       string
}

type replica struct {
	prefillFreeAt float64
	inFlight      int
	kvUsed        int
}

type event struct {
	at  float64
	seq int
	fn  func()
}

type eventHeap []*event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(*event)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	ev := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return ev
}

type engine struct {
	cfg    Config
	model  cost.Model
	policy sched.Policy
	belief *prefix.Directory
	actual *prefix.Directory
	coal   *coalesce.Coalescer
	base   time.Time
	now    float64

	reps    []*replica
	events  eventHeap
	seq     int
	results []Result
	seen    map[string]int
}

// Run executes a trace and returns request results.
func Run(cfg Config, tr Trace) ([]Result, error) {
	e, err := newEngine(cfg)
	if err != nil {
		return nil, err
	}
	for _, req := range tr.Requests {
		e.schedule(req.ArrivalMS, func() { e.onArrival(req) })
	}
	for e.events.Len() > 0 {
		ev := heap.Pop(&e.events).(*event)
		if ev.at > e.now {
			e.now = ev.at
		}
		ev.fn()
	}
	return e.results, nil
}

func newEngine(cfg Config) (*engine, error) {
	cfg = cfg.normalized()
	policy, err := sched.New(cfg.PolicyName, cfg.Cost)
	if err != nil {
		return nil, err
	}
	e := &engine{
		cfg:    cfg,
		model:  cfg.Cost,
		policy: policy,
		belief: prefix.NewDirectory(cfg.CacheBlocks, cfg.BlockTokens),
		actual: prefix.NewDirectory(cfg.CacheBlocks, cfg.BlockTokens),
		base:   time.Unix(0, 0).UTC(),
		reps:   make([]*replica, cfg.Replicas),
		seen:   make(map[string]int),
	}
	for i := range e.reps {
		e.reps[i] = &replica{}
	}
	heap.Init(&e.events)
	e.coal = coalesce.New(cfg.Cost, coalesce.Config{
		EpsilonMS:      cfg.EpsilonMS,
		DeltaMaxMS:     cfg.DeltaMaxMS,
		DefaultSlackMS: cfg.DefaultSlackMS,
		Now:            func() time.Time { return e.base.Add(durMS(e.now)) },
	})
	return e, nil
}

func (e *engine) schedule(at float64, fn func()) {
	e.seq++
	heap.Push(&e.events, &event{at: at, seq: e.seq, fn: fn})
}

func (e *engine) onArrival(r Request) {
	key := uint64(0)
	if len(r.Chain) > 0 {
		key = r.Chain[len(r.Chain)-1]
	}
	seen := e.seen[r.Group]
	e.seen[r.Group] = seen + 1
	follower := seen > 0

	slack := 10000.0
	if r.Stream {
		slack = e.cfg.DefaultSlackMS
	}
	var leader *coalesce.Group
	if e.cfg.Coalesce {
		leader = e.coal.Registry().Get(key)
	}
	if leader != nil {
		states := e.snapshot(r.Chain)
		leaderRemaining := math.Max(0, float64(leader.EstDone.Sub(e.base).Microseconds())/1000-e.now)
		waitLeader := math.Max(0, e.reps[leader.Replica].prefillFreeAt-e.now-leaderRemaining)
		waitBest := math.Inf(1)
		for i, st := range states {
			if i == leader.Replica || st.Unhealthy {
				continue
			}
			waitBest = math.Min(waitBest, st.QueuedMS)
		}
		if math.IsInf(waitBest, 1) {
			waitBest = 0
		}
		d := e.coal.Decide(coalesce.Arrival{
			Key:          key,
			QTokens:      0,
			SharedTokens: r.PromptTokens,
			WaitLeaderMS: waitLeader,
			WaitBestMS:   waitBest,
			SlackMS:      slack,
		})
		switch d.Action {
		case coalesce.Hold:
			holdStart := e.now
			readyAt := float64(leader.EstDone.Sub(e.base).Microseconds()) / 1000
			// The group deadline is stored at nanosecond precision and rounded
			// down when converted back, so wake just after it.
			wake := math.Min(readyAt+0.002, e.now+d.Deadline.Seconds()*1000)
			e.schedule(wake, func() { e.wakeHeld(r, leader, follower, holdStart) })
			return
		case coalesce.Spill:
			e.dispatch(r, -1, -1, follower, true, "spilled", 0)
			return
		}
	}

	info := e.dispatch(r, -1, -1, follower, false, "leader", 0)
	if e.cfg.Coalesce {
		g, fresh := e.coal.Registry().Start(key, info.replica,
			e.base.Add(durMS(e.now)), durMS(info.readyAt-e.now))
		if fresh {
			e.schedule(info.readyAt, func() { e.coal.Registry().Complete(g, coalesce.Ready) })
		}
	}
}

func (e *engine) wakeHeld(r Request, g *coalesce.Group, follower bool, holdStart float64) {
	holdMS := e.now - holdStart
	ready := false
	select {
	case <-g.Done():
		ready = g.Outcome() == coalesce.Ready
	default:
	}
	if ready {
		e.dispatch(r, g.Replica, r.PromptTokens, follower, true, "released", holdMS)
		return
	}
	e.dispatch(r, -1, -1, follower, true, "deadline", holdMS)
}

type dispatchInfo struct {
	replica   int
	startMS   float64
	prefillMS float64
	readyAt   float64
}

func (e *engine) dispatch(r Request, force, cachedOverride int, follower, coalesced bool, holdOutcome string, holdMS float64) dispatchInfo {
	states := e.snapshot(r.Chain)
	idx := force
	if idx < 0 || idx >= len(states) {
		idx, _ = e.policy.Pick(sched.Request{
			ID:           r.ID,
			Key:          chainKey(r.Chain),
			QTokens:      r.PromptTokens,
			DecodeTokens: r.OutputTokens,
			Stream:       r.Stream,
			SlackMS:      2000,
		}, states)
	}
	idx = min(max(idx, 0), len(states)-1)
	actualCached := e.actual.Hit(idx, r.Chain)
	cached := actualCached
	if cachedOverride >= 0 {
		cached = cachedOverride
	}
	cached = min(max(cached, 0), r.PromptTokens)
	unc := r.PromptTokens - cached
	prefill := e.model.Cost(unc, cached)
	start := math.Max(e.now, e.reps[idx].prefillFreeAt)
	decode := float64(r.OutputTokens) * e.cfg.DecodeMS
	readyAt := start + prefill
	done := readyAt + decode
	rep := e.reps[idx]
	rep.prefillFreeAt = readyAt
	rep.inFlight++
	rep.kvUsed += r.PromptTokens + r.OutputTokens
	kvAdd := r.PromptTokens + r.OutputTokens
	e.schedule(done, func() {
		rep.inFlight = max(0, rep.inFlight-1)
		rep.kvUsed = max(0, rep.kvUsed-kvAdd)
	})
	e.schedule(readyAt, func() { e.actual.Record(idx, r.Chain) })
	// The gateway's directory is speculative: it records at routing time.
	if e.cfg.Speculative {
		e.belief.Record(idx, r.Chain)
	} else {
		e.schedule(readyAt, func() { e.belief.Record(idx, r.Chain) })
	}

	wait := start - r.ArrivalMS
	res := Result{
		ID:           r.ID,
		Group:        r.Group,
		PromptTokens: r.PromptTokens,
		OutputTokens: r.OutputTokens,
		Stream:       r.Stream,
		Replica:      idx,
		Follower:     follower,
		Coalesced:    coalesced,
		Hold:         holdMS > 0,
		HoldMS:       holdMS,
		HoldOutcome:  holdOutcome,
		CachedTokens: cached,
		ArrivalMS:    r.ArrivalMS,
		DispatchMS:   e.now,
		StartMS:      start,
		PrefillMS:    prefill,
		QueueWaitMS:  wait,
		TTFTMS:       wait + prefill,
		DoneMS:       done,
		Policy:       e.policy.Name(),
	}
	e.results = append(e.results, res)
	return dispatchInfo{replica: idx, startMS: start, prefillMS: prefill, readyAt: readyAt}
}

func (e *engine) snapshot(chain []uint64) []sched.Replica {
	out := make([]sched.Replica, len(e.reps))
	for i, rep := range e.reps {
		out[i] = sched.Replica{
			ID:           fmt.Sprintf("sim-%d", i),
			InFlight:     rep.inFlight,
			QueuedMS:     math.Max(0, rep.prefillFreeAt-e.now),
			KVUsed:       rep.kvUsed,
			KVMax:        e.cfg.KVMax,
			CachedTokens: e.belief.Hit(i, chain),
			Speed:        1,
		}
	}
	return out
}

func chainKey(chain []uint64) uint64 {
	if len(chain) == 0 {
		return 0
	}
	return chain[len(chain)-1]
}

func durMS(ms float64) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// Summary aggregates one run.
type Summary struct {
	Policy            string
	Coalesce          bool
	Replicas          int
	Requests          int
	TTFTMean          float64
	P50               float64
	P95               float64
	P99               float64
	FollowerP50       float64
	FollowerP99       float64
	PrefillMS         float64
	PrefillTokens     int
	UncachedTokens    int
	DuplicateTokens   int
	DuplicateRatio    float64
	SLOGoodput        float64
	StreamP99         float64
	BatchP99Wait      float64
	BatchMaxWait      float64
	CoalescedRequests int
	HoldRequests      int
	HoldReleased      int
}

// Summarize computes metrics for a completed run.
func Summarize(cfg Config, tr Trace, results []Result) Summary {
	cfg = cfg.normalized()
	s := Summary{
		Policy:   cfg.PolicyName,
		Coalesce: cfg.Coalesce,
		Replicas: cfg.Replicas,
		Requests: len(results),
	}
	var ttfts, followers, streamTTFT, batchWait []float64
	for _, r := range results {
		ttfts = append(ttfts, r.TTFTMS)
		if r.Stream {
			streamTTFT = append(streamTTFT, r.TTFTMS)
		} else {
			batchWait = append(batchWait, r.QueueWaitMS)
		}
		if r.Follower {
			followers = append(followers, r.TTFTMS)
			if r.CachedTokens == 0 {
				s.DuplicateTokens += r.PromptTokens
			}
		}
		s.PrefillMS += r.PrefillMS
		s.PrefillTokens += r.PromptTokens
		s.UncachedTokens += r.PromptTokens - r.CachedTokens
		if r.Coalesced {
			s.CoalescedRequests++
		}
		if r.HoldOutcome == "released" {
			s.HoldReleased++
		}
		if r.Hold {
			s.HoldRequests++
		}
	}
	s.TTFTMean = mean(ttfts)
	s.P50 = percentile(ttfts, 50)
	s.P95 = percentile(ttfts, 95)
	s.P99 = percentile(ttfts, 99)
	s.FollowerP50 = percentile(followers, 50)
	s.FollowerP99 = percentile(followers, 99)
	s.StreamP99 = percentile(streamTTFT, 99)
	s.BatchP99Wait = percentile(batchWait, 99)
	s.BatchMaxWait = maxFloat(batchWait)
	if s.UncachedTokens > 0 {
		s.DuplicateRatio = float64(s.DuplicateTokens) / float64(s.UncachedTokens)
	}
	durSec := tr.DurationMS / 1000
	if durSec <= 0 && len(results) > 0 {
		durSec = percentile([]float64{lastDone(results)}, 100) / 1000
	}
	if durSec > 0 {
		var ok int
		for _, r := range results {
			if r.Stream && r.TTFTMS <= cfg.SLOTargetMS {
				ok++
			}
		}
		s.SLOGoodput = float64(ok) / durSec
	}
	return s
}

func lastDone(results []Result) float64 {
	last := 0.0
	for _, r := range results {
		if r.DoneMS > last {
			last = r.DoneMS
		}
	}
	return last
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	var sum float64
	for _, x := range v {
		sum += x
	}
	return sum / float64(len(v))
}

func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	rank := p / 100 * float64(len(c)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return c[lo]
	}
	w := rank - float64(lo)
	return c[lo]*(1-w) + c[hi]*w
}
