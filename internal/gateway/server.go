package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ab1nv/bales/internal/coalesce"
	"github.com/ab1nv/bales/internal/engine"
	"github.com/ab1nv/bales/internal/prefix"
	"github.com/ab1nv/bales/internal/queue"
	"github.com/ab1nv/bales/internal/sched"
	"github.com/ab1nv/bales/internal/telemetry"
)

// Server is the Bales gateway.
type Server struct {
	cfg      Config
	adapters []engine.Adapter
	policy   sched.Policy
	q        *queue.Queue
	coal     *coalesce.Coalescer
	dir      *prefix.Directory
	audit    *prefix.Auditor
	rec      *telemetry.Recorder

	mu    sync.Mutex
	loads []*replicaLoad

	ids       atomic.Uint64
	counters  counters
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
}

type replicaLoad struct {
	adapter    engine.Adapter
	inFlight   int
	queuedMS   float64
	localKV    int
	reportedKV int
	kvMax      int
	healthy    bool
	failures   int
}

type counters struct {
	Holds        atomic.Uint64
	Spills       atomic.Uint64
	HoldReleased atomic.Uint64
	Deadlines    atomic.Uint64
	LeaderReady  atomic.Uint64
	LeaderFailed atomic.Uint64
	Rejected     atomic.Uint64
}

// New builds a gateway and starts its dispatcher and metrics loops.
func New(cfg Config, rec *telemetry.Recorder) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	adapters := make([]engine.Adapter, len(cfg.Replicas))
	for i, url := range cfg.Replicas {
		adapters[i] = engine.NewOpenAI(fmt.Sprintf("replica-%d", i), url)
	}
	policy, err := sched.New(cfg.Policy, cfg.Cost)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:      cfg,
		adapters: adapters,
		policy:   policy,
		q:        queue.New(queue.Config{DStream: cfg.DStream, DBatch: cfg.DBatch, Eta: cfg.Eta, QMaxMS: cfg.QMaxMS}),
		coal:     coalesce.New(cfg.Cost, cfg.Coalesce),
		dir:      prefix.NewDirectory(cfg.DirectoryBlocks, cfg.BlockTokens),
		audit:    &prefix.Auditor{},
		rec:      rec,
		loads:    make([]*replicaLoad, len(adapters)),
		ctx:      ctx,
		cancel:   cancel,
	}
	for i, a := range adapters {
		s.loads[i] = &replicaLoad{adapter: a, kvMax: cfg.KVMaxTokens, healthy: true}
	}
	s.wg.Go(s.dispatchLoop)
	if cfg.MetricsInterval > 0 {
		s.wg.Go(s.refreshLoop)
	}
	return s, nil
}

// Close stops background loops and flushes telemetry. It is safe to call twice.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		s.wg.Wait()
		if s.rec != nil {
			s.rec.Close()
		}
	})
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /debug/stats", s.handleStats)
	return mux
}

type chatRequest struct {
	Model    string    `json:"model"`
	Stream   bool      `json:"stream"`
	Messages []message `json:"messages"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (m message) text() string {
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" || p.Type == "" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

func promptText(msgs []message) string {
	var b strings.Builder
	for _, m := range msgs {
		if t := m.text(); t != "" {
			b.WriteString(m.Role)
			b.WriteString(": ")
			b.WriteString(t)
			b.WriteString("\n")
		}
	}
	return b.String()
}

type pendingReq struct {
	ctx          context.Context
	id           string
	tenant       string
	class        string
	model        string
	body         []byte
	promptTokens int
	chain        []uint64
	key          uint64
	groupID      string
	stream       bool
	slackMS      float64
	arrival      time.Time
	ch           chan assignment
}

type assignment struct {
	replica      int
	policyReason string
	cachedTokens int
	prefillEstMS float64
	kvAdd        int
	hold         bool
	leader       *coalesce.Group
	holdDeadline time.Duration
	coalesced    bool
	holdOutcome  string
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "cannot read request body")
		return
	}
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	prompt := promptText(req.Messages)
	if prompt == "" {
		prompt = " "
	}
	tokens := max(1, int(math.Ceil(float64(len(prompt))*s.cfg.TokensPerByte)))
	tenant := headerOr(r, "X-Bales-Tenant", "default")
	hasher := prefix.NewHasher(tenant+"|"+s.cfg.TenantSalt, s.cfg.BlockBytes)
	chain := hasher.Chain([]byte(prompt))
	var key uint64
	if len(chain) > 0 {
		key = chain[len(chain)-1]
	}
	class := "stream"
	qclass := queue.Stream
	slack := 2000.0
	if strings.EqualFold(r.Header.Get("X-Bales-Class"), "batch") {
		class = "batch"
		qclass = queue.Batch
		slack = 10000
	}
	if v := r.Header.Get("X-Bales-Slack-MS"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			slack = f
		}
	}
	reqID := r.Header.Get("X-Request-ID")
	if reqID == "" {
		reqID = fmt.Sprintf("req-%d", s.ids.Add(1))
	}
	modelName := req.Model
	if modelName == "" {
		modelName = "unknown"
	}
	pr := &pendingReq{
		ctx:          r.Context(),
		id:           reqID,
		tenant:       tenant,
		class:        class,
		model:        modelName,
		body:         body,
		promptTokens: tokens,
		chain:        chain,
		key:          key,
		groupID:      fmt.Sprintf("%016x", key),
		stream:       req.Stream,
		slackMS:      slack,
		arrival:      time.Now(),
		ch:           make(chan assignment),
	}
	est := s.cfg.Cost.Cost(tokens, 0) + float64(s.cfg.DecodeTokens)*2
	if est <= 0 {
		est = 1
	}
	if _, err := s.q.Push(queue.Request{
		ID:        pr.id,
		Arrival:   pr.arrival,
		Class:     qclass,
		EstCostMS: est,
		Slack:     time.Duration(slack * float64(time.Millisecond)),
		Meta:      pr,
	}); err != nil {
		if errors.Is(err, queue.ErrOverloaded) {
			s.counters.Rejected.Add(1)
			writeJSONError(w, http.StatusTooManyRequests, "admission queue is full")
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	var as assignment
	select {
	case as = <-pr.ch:
	case <-r.Context().Done():
		return
	}
	queueWaitMS := float64(time.Since(pr.arrival).Microseconds()) / 1000

	holdMS := 0.0
	if as.hold {
		holdStart := time.Now()
		timer := time.NewTimer(as.holdDeadline)
		if as.holdDeadline <= 0 {
			timer.Stop()
		}
		holdOutcome := "released"
		var timerC <-chan time.Time
		if as.holdDeadline > 0 {
			timerC = timer.C
		}
		select {
		case <-as.leader.Done():
			if as.leader.Outcome() != coalesce.Ready {
				holdOutcome = "failed"
			}
		case <-timerC:
			holdOutcome = "deadline"
			s.counters.Deadlines.Add(1)
		case <-r.Context().Done():
			timer.Stop()
			return
		}
		timer.Stop()
		holdMS = float64(time.Since(holdStart).Microseconds()) / 1000
		if holdOutcome == "released" {
			s.counters.HoldReleased.Add(1)
			as = s.route(pr, as.leader.Replica, pr.promptTokens)
			as.holdOutcome = "released"
		} else {
			as = s.route(pr, -1, -1)
			as.holdOutcome = holdOutcome
		}
		as.coalesced = true
	}
	if r.Context().Err() != nil {
		s.rollback(as)
		return
	}
	defer s.finishAssignment(as)
	s.proxy(w, pr, as, queueWaitMS, holdMS)
}

func (s *Server) proxy(w http.ResponseWriter, pr *pendingReq, as assignment, queueWaitMS, holdMS float64) {
	if as.replica < 0 || as.replica >= len(s.loads) {
		writeJSONError(w, http.StatusBadGateway, "no replica assigned")
		return
	}
	adapter := s.loads[as.replica].adapter
	var first atomic.Bool
	onFirst := func() {
		first.Store(true)
		if as.leader != nil {
			s.coal.Registry().Complete(as.leader, coalesce.Ready)
			s.counters.LeaderReady.Add(1)
		}
	}
	out, err := adapter.Chat(pr.ctx, pr.body, w, onFirst)
	if as.leader != nil && !first.Load() {
		s.coal.Registry().Complete(as.leader, coalesce.Failed)
		s.counters.LeaderFailed.Add(1)
	}
	if out.Status == http.StatusOK {
		s.audit.Observe(as.cachedTokens, out.CachedTokens)
	}
	status := out.Status
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
		if status == 0 {
			writeJSONError(w, http.StatusBadGateway, "upstream request failed")
			status = http.StatusBadGateway
		}
	}
	if s.rec != nil {
		_ = s.rec.Record(telemetry.Row{
			Time:             time.Now(),
			RequestID:        pr.id,
			Tenant:           pr.tenant,
			Class:            pr.class,
			Model:            pr.model,
			PromptTokens:     pr.promptTokens,
			PredictedCached:  as.cachedTokens,
			EngineCached:     out.CachedTokens,
			GroupID:          pr.groupID,
			Replica:          fmt.Sprintf("%d", as.replica),
			Policy:           s.policy.Name(),
			PolicyReason:     as.policyReason,
			Coalesced:        as.coalesced,
			HoldMS:           holdMS,
			HoldOutcome:      as.holdOutcome,
			QueueWaitMS:      queueWaitMS,
			PrefillEstMS:     as.prefillEstMS,
			TTFTMS:           out.FirstTokenMS,
			TotalMS:          out.TotalMS,
			CompletionTokens: out.CompletionTokens,
			Status:           status,
			Stream:           pr.stream,
			SlackMS:          pr.slackMS,
			Err:              errMsg,
		})
	}
}

func (s *Server) dispatchLoop() {
	for {
		it, ok := s.q.PopWait(s.ctx)
		if !ok {
			return
		}
		pr, ok := it.Req.Meta.(*pendingReq)
		if !ok {
			continue
		}
		s.dispatch(pr)
	}
}

func (s *Server) dispatch(pr *pendingReq) {
	if pr.ctx.Err() != nil {
		return
	}
	states := s.snapshot(pr.chain)
	leader := s.coal.Registry().Get(pr.key)
	if leader != nil {
		waitLeader := 0.0
		if leader.Replica >= 0 && leader.Replica < len(states) {
			leaderEst := leader.EstDone.Sub(leader.Started).Seconds() * 1000
			waitLeader = math.Max(0, states[leader.Replica].QueuedMS-leaderEst)
		}
		waitBest := math.Inf(1)
		for i, r := range states {
			if i == leader.Replica || r.Unhealthy {
				continue
			}
			waitBest = math.Min(waitBest, r.QueuedMS)
		}
		if math.IsInf(waitBest, 1) {
			waitBest = 0
		}
		d := s.coal.Decide(coalesce.Arrival{
			Key:          pr.key,
			QTokens:      pr.promptTokens,
			SharedTokens: pr.promptTokens,
			WaitLeaderMS: waitLeader,
			WaitBestMS:   waitBest,
			SlackMS:      pr.slackMS,
		})
		switch d.Action {
		case coalesce.Hold:
			s.counters.Holds.Add(1)
			s.send(pr, assignment{
				replica:      -1,
				hold:         true,
				leader:       leader,
				holdDeadline: d.Deadline,
				policyReason: d.Reason,
				coalesced:    true,
				holdOutcome:  "pending",
			})
			return
		case coalesce.Spill:
			s.counters.Spills.Add(1)
			as := s.route(pr, -1, -1)
			as.policyReason = d.Reason + "; " + as.policyReason
			as.coalesced = true
			as.holdOutcome = "spilled"
			s.send(pr, as)
			return
		}
	}
	as := s.route(pr, -1, -1)
	g, fresh := s.coal.Registry().Start(pr.key, as.replica, time.Now(), durationMS(as.prefillEstMS))
	if fresh {
		as.leader = g
		as.holdOutcome = "leader"
	} else {
		as.holdOutcome = "spilled"
	}
	s.send(pr, as)
}

func (s *Server) route(pr *pendingReq, force, cachedOverride int) assignment {
	states := s.snapshot(pr.chain)
	if len(states) == 0 {
		return assignment{replica: -1, policyReason: "no replicas", holdOutcome: "na"}
	}
	idx := force
	reason := "leader cache"
	if idx < 0 || idx >= len(states) {
		idx, reason = s.policy.Pick(sched.Request{
			ID:           pr.id,
			Key:          pr.key,
			QTokens:      pr.promptTokens,
			DecodeTokens: s.cfg.DecodeTokens,
			Stream:       pr.class == "stream",
			SlackMS:      pr.slackMS,
		}, states)
	}
	idx = min(max(idx, 0), len(states)-1)
	cached := states[idx].CachedTokens
	if cachedOverride >= 0 {
		cached = cachedOverride
	}
	cached = min(max(cached, 0), pr.promptTokens)
	unc := pr.promptTokens - cached
	prefill := s.cfg.Cost.Cost(unc, cached)
	kvAdd := unc + s.cfg.DecodeTokens
	s.addLoad(idx, prefill, kvAdd)
	s.dir.Record(idx, pr.chain)
	return assignment{
		replica:      idx,
		policyReason: reason,
		cachedTokens: cached,
		prefillEstMS: prefill,
		kvAdd:        kvAdd,
	}
}

func (s *Server) snapshot(chain []uint64) []sched.Replica {
	s.mu.Lock()
	defer s.mu.Unlock()
	reps := make([]sched.Replica, len(s.loads))
	for i, l := range s.loads {
		reps[i] = sched.Replica{
			ID:           l.adapter.Name(),
			InFlight:     l.inFlight,
			QueuedMS:     l.queuedMS,
			KVUsed:       max(l.localKV, l.reportedKV),
			KVMax:        l.kvMax,
			CachedTokens: s.dir.Hit(i, chain),
			Speed:        1,
			Unhealthy:    !l.healthy,
		}
	}
	return reps
}

func (s *Server) addLoad(idx int, prefillMS float64, kvAdd int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 0 || idx >= len(s.loads) {
		return
	}
	l := s.loads[idx]
	l.inFlight++
	l.queuedMS += prefillMS
	l.localKV += kvAdd
}

func (s *Server) finishAssignment(as assignment) {
	if as.hold || as.replica < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if as.replica >= len(s.loads) {
		return
	}
	l := s.loads[as.replica]
	l.inFlight = max(0, l.inFlight-1)
	l.queuedMS = math.Max(0, l.queuedMS-as.prefillEstMS)
	l.localKV = max(0, l.localKV-as.kvAdd)
}

func (s *Server) rollback(as assignment) {
	s.finishAssignment(as)
	if as.leader != nil {
		s.coal.Registry().Complete(as.leader, coalesce.Failed)
	}
}

func (s *Server) send(pr *pendingReq, as assignment) {
	select {
	case pr.ch <- as:
	case <-pr.ctx.Done():
		s.rollback(as)
	case <-s.ctx.Done():
		s.rollback(as)
	}
}

func (s *Server) refreshLoop() {
	ticker := time.NewTicker(s.cfg.MetricsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			for _, l := range s.loads {
				m, err := l.adapter.Metrics(s.ctx)
				s.mu.Lock()
				if err != nil {
					l.failures++
					if l.failures >= 3 {
						l.healthy = false
					}
				} else {
					l.failures = 0
					l.healthy = true
					l.reportedKV = int(m.KVCacheUsage * float64(l.kvMax))
					l.inFlight = max(l.inFlight, m.Running)
				}
				s.mu.Unlock()
			}
		}
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`+"\n")
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	st := s.q.Stats()
	a := s.audit.Snapshot()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "bales_queue_depth %d\n", st.Queued)
	fmt.Fprintf(w, "bales_queue_queued_ms %f\n", st.QueuedMS)
	fmt.Fprintf(w, "bales_requests_admitted_total %d\n", st.Admitted)
	fmt.Fprintf(w, "bales_requests_rejected_total %d\n", st.Rejected)
	fmt.Fprintf(w, "bales_coalesce_holds_total %d\n", s.counters.Holds.Load())
	fmt.Fprintf(w, "bales_coalesce_spills_total %d\n", s.counters.Spills.Load())
	fmt.Fprintf(w, "bales_hold_released_total %d\n", s.counters.HoldReleased.Load())
	fmt.Fprintf(w, "bales_hold_deadline_total %d\n", s.counters.Deadlines.Load())
	fmt.Fprintf(w, "bales_leader_ready_total %d\n", s.counters.LeaderReady.Load())
	fmt.Fprintf(w, "bales_leader_failed_total %d\n", s.counters.LeaderFailed.Load())
	fmt.Fprintf(w, "bales_audit_precision %f\n", a.Precision)
	fmt.Fprintf(w, "bales_audit_recall %f\n", a.Recall)
	for i := range s.loads {
		fmt.Fprintf(w, "bales_directory_blocks{replica=%q} %d\n", s.loads[i].adapter.Name(), s.dir.Size(i))
	}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	loads := make([]map[string]any, len(s.loads))
	for i, l := range s.loads {
		loads[i] = map[string]any{
			"id":         l.adapter.Name(),
			"inflight":   l.inFlight,
			"queued_ms":  l.queuedMS,
			"kv_used":    max(l.localKV, l.reportedKV),
			"kv_max":     l.kvMax,
			"healthy":    l.healthy,
			"dir_blocks": s.dir.Size(i),
		}
	}
	s.mu.Unlock()
	out := map[string]any{
		"policy": s.policy.Name(),
		"queue":  s.q.Stats(),
		"audit":  s.audit.Snapshot(),
		"counters": map[string]uint64{
			"holds":         s.counters.Holds.Load(),
			"spills":        s.counters.Spills.Load(),
			"released":      s.counters.HoldReleased.Load(),
			"deadlines":     s.counters.Deadlines.Load(),
			"leader_ready":  s.counters.LeaderReady.Load(),
			"leader_failed": s.counters.LeaderFailed.Load(),
		},
		"replicas": loads,
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": msg, "type": "bales_error"},
	})
}

func headerOr(r *http.Request, key, fallback string) string {
	if v := r.Header.Get(key); v != "" {
		return v
	}
	return fallback
}

func durationMS(ms float64) time.Duration {
	if ms <= 0 {
		ms = 1
	}
	return time.Duration(ms * float64(time.Millisecond))
}
