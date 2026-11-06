package sim

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/ab1nv/bales/internal/sched"
)

// SummaryRow is one experiment observation.
type SummaryRow struct {
	Experiment        string
	Policy            string
	Parameter         string
	Coalesce          bool
	Seed              int64
	Replicas          int
	Requests          int
	MeanTTFTMS        float64
	P50MS             float64
	P95MS             float64
	P99MS             float64
	FollowerP50MS     float64
	FollowerP99MS     float64
	PrefillMS         float64
	DuplicateRatio    float64
	SLOGoodput        float64
	StreamP99MS       float64
	BatchP99WaitMS    float64
	BatchMaxWaitMS    float64
	CoalescedRequests int
	HoldRequests      int
	HoldReleased      int
	GateAction        string
	GateReplicas      int
}

type spec struct {
	label    string
	policy   string
	coalesce bool
}

type rawResult struct {
	run      string
	policy   string
	coalesce bool
	res      Result
}

// RunExperiment runs one named experiment and writes its CSVs under outDir.
// exp is all, burst, load, longctx, affinity, ablation or queue.
func RunExperiment(exp, outDir string, seeds, replicas int) error {
	if seeds < 1 {
		seeds = 1
	}
	if replicas < 1 {
		replicas = 4
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	var all []SummaryRow
	add := func(rows []SummaryRow, err error) error {
		if err != nil {
			return err
		}
		all = append(all, rows...)
		return nil
	}
	if exp == "all" || exp == "burst" {
		rows, raw, err := runBurst(replicas)
		if err := add(rows, err); err != nil {
			return err
		}
		if err := writeSummary(filepath.Join(outDir, "burst_summary.csv"), rows); err != nil {
			return err
		}
		if err := writeRaw(filepath.Join(outDir, "burst_requests.csv"), raw); err != nil {
			return err
		}
	}
	if exp == "all" || exp == "load" {
		rows, err := runLoad(replicas, seeds)
		if err := add(rows, err); err != nil {
			return err
		}
		if err := writeSummary(filepath.Join(outDir, "load_summary.csv"), rows); err != nil {
			return err
		}
	}
	if exp == "all" || exp == "longctx" {
		rows, err := runLongContext(replicas, seeds)
		if err := add(rows, err); err != nil {
			return err
		}
		if err := writeSummary(filepath.Join(outDir, "longctx_summary.csv"), rows); err != nil {
			return err
		}
	}
	if exp == "all" || exp == "affinity" {
		rows, err := runAffinity(replicas, seeds)
		if err := add(rows, err); err != nil {
			return err
		}
		if err := writeSummary(filepath.Join(outDir, "affinity_summary.csv"), rows); err != nil {
			return err
		}
	}
	if exp == "all" || exp == "ablation" {
		rows, err := runAblation(replicas)
		if err := add(rows, err); err != nil {
			return err
		}
		if err := writeSummary(filepath.Join(outDir, "ablation_summary.csv"), rows); err != nil {
			return err
		}
	}
	if exp == "all" || exp == "queue" {
		rows := runQueuePoints(seeds)
		all = append(all, rows...)
		if err := writeSummary(filepath.Join(outDir, "queue_summary.csv"), rows); err != nil {
			return err
		}
	}
	if err := writeSummary(filepath.Join(outDir, "summary.csv"), all); err != nil {
		return err
	}
	return nil
}

func runBurst(replicas int) ([]SummaryRow, []rawResult, error) {
	tr := BurstTrace(32, 2048, 32, 3)
	specs := []spec{
		{"round_robin", "round_robin", false},
		{"llmd_style", "llmd_style", false},
		{"bales_ect_linear", "bales_ect_linear", false},
		{"bales_ect", "bales_ect", false},
		{"bales_ect_coalesced", "bales_ect", true},
	}
	var rows []SummaryRow
	var raw []rawResult
	for _, sp := range specs {
		cfg := DefaultConfig()
		cfg.Replicas = replicas
		cfg.PolicyName = sp.policy
		cfg.Coalesce = sp.coalesce
		res, err := Run(cfg, tr)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, toRow("burst", sp, "stagger=3ms", 1, tr, cfg, res))
		for _, r := range res {
			raw = append(raw, rawResult{run: sp.label, policy: sp.policy, coalesce: sp.coalesce, res: r})
		}
	}
	return rows, raw, nil
}

func runLoad(replicas, seeds int) ([]SummaryRow, error) {
	rates := []float64{4, 8, 12, 16, 20}
	specs := []spec{
		{"round_robin", "round_robin", false},
		{"llmd_style", "llmd_style", false},
		{"bales_ect_linear", "bales_ect_linear", false},
		{"bales_ect", "bales_ect", false},
	}
	var rows []SummaryRow
	for seed := 1; seed <= seeds; seed++ {
		for _, rate := range rates {
			tr := LoadTrace(rate, 6000, int64(seed))
			for _, sp := range specs {
				cfg := DefaultConfig()
				cfg.Replicas = replicas
				cfg.PolicyName = sp.policy
				cfg.Coalesce = sp.coalesce
				res, err := Run(cfg, tr)
				if err != nil {
					return nil, err
				}
				rows = append(rows, toRow("load", sp, fmt.Sprintf("rate=%.0f", rate), int64(seed), tr, cfg, res))
			}
		}
	}
	return rows, nil
}

func runLongContext(replicas, seeds int) ([]SummaryRow, error) {
	rates := []float64{6, 10}
	specs := []spec{
		{"least_tokens", "least_tokens", false},
		{"bales_ect_linear", "bales_ect_linear", false},
		{"bales_ect", "bales_ect", false},
	}
	var rows []SummaryRow
	for seed := 1; seed <= seeds; seed++ {
		for _, rate := range rates {
			tr := LongContextTrace(rate, 6000, int64(seed))
			for _, sp := range specs {
				cfg := DefaultConfig()
				cfg.Replicas = replicas
				cfg.PolicyName = sp.policy
				cfg.Coalesce = sp.coalesce
				cfg.SLOTargetMS = 3000
				res, err := Run(cfg, tr)
				if err != nil {
					return nil, err
				}
				rows = append(rows, toRow("longctx", sp, fmt.Sprintf("rate=%.0f", rate), int64(seed), tr, cfg, res))
			}
		}
	}
	return rows, nil
}

func runAffinity(replicas, seeds int) ([]SummaryRow, error) {
	traces := []struct {
		name   string
		blocks int
		hot    float64
	}{
		{"aff-short-hot", 2, 0.9},
		{"aff-medium-hot", 4, 0.7},
		{"aff-long-hot", 8, 0.9},
		{"aff-long-mixed", 8, 0.5},
		{"aff-verylong-cold", 16, 0.25},
		{"aff-medium-mixed", 4, 0.5},
	}
	specs := []spec{
		{"round_robin", "round_robin", false},
		{"cacheroute_style", "cacheroute_style", false},
		{"bales_ect", "bales_ect", false},
	}
	var rows []SummaryRow
	for seed := 1; seed <= seeds; seed++ {
		for _, tc := range traces {
			tr := AffinityTrace(tc.name, int64(seed), tc.blocks, tc.hot)
			action, count := sched.DefaultGateConfig().Decide(sched.GroupStat{
				RatePerSec:       10 * tc.hot,
				MeanSuffixTokens: 2 * BlockTokens,
				PrefixTokens:     tc.blocks * BlockTokens,
				Replicas:         replicas,
			}, DefaultConfig().Cost, 1000)
			for _, sp := range specs {
				cfg := DefaultConfig()
				cfg.Replicas = replicas
				cfg.PolicyName = sp.policy
				cfg.Coalesce = sp.coalesce
				cfg.SLOTargetMS = 2000
				res, err := Run(cfg, tr)
				if err != nil {
					return nil, err
				}
				row := toRow("affinity", sp, tc.name, int64(seed), tr, cfg, res)
				row.GateAction = action.String()
				row.GateReplicas = count
				rows = append(rows, row)
			}
		}
	}
	return rows, nil
}

func runAblation(replicas int) ([]SummaryRow, error) {
	tr := BurstTrace(32, 2048, 32, 3)
	specs := []spec{
		{"bales_score", "bales_score", false},
		{"bales_ect_linear", "bales_ect_linear", false},
		{"bales_ect", "bales_ect", false},
		{"bales_ect_coalesced", "bales_ect", true},
	}
	var rows []SummaryRow
	for _, sp := range specs {
		cfg := DefaultConfig()
		cfg.Replicas = replicas
		cfg.PolicyName = sp.policy
		cfg.Coalesce = sp.coalesce
		res, err := Run(cfg, tr)
		if err != nil {
			return nil, err
		}
		rows = append(rows, toRow("ablation", sp, "stagger=3ms", 1, tr, cfg, res))
	}
	return rows, nil
}

func runQueuePoints(seeds int) []SummaryRow {
	rates := []float64{5, 8, 11, 14}
	type qspec struct {
		policy   string
		deadline float64
	}
	specs := []qspec{
		{"fifo", 5000},
		{"strict_priority", 5000},
		{"edf", 5000},
		{"wfq_stride", 5000},
		{"edf", 800},
	}
	var rows []SummaryRow
	for seed := 1; seed <= seeds; seed++ {
		for _, rate := range rates {
			for _, sp := range specs {
				q := RunQueue(rate, 20000, int64(seed), sp.policy, sp.deadline)
				label := sp.policy
				if sp.policy == "edf" && sp.deadline != 5000 {
					label = fmt.Sprintf("edf_batch_%.0f", sp.deadline)
				}
				rows = append(rows, SummaryRow{
					Experiment:     "queue",
					Policy:         label,
					Parameter:      fmt.Sprintf("rate=%.0f", rate),
					Seed:           int64(seed),
					Replicas:       1,
					Requests:       q.Streams + q.Batches,
					P50MS:          q.StreamP50,
					StreamP99MS:    q.StreamP99,
					P99MS:          q.StreamP99,
					BatchP99WaitMS: q.BatchP99Wait,
					BatchMaxWaitMS: q.BatchMaxWait,
					SLOGoodput:     q.Goodput,
				})
			}
		}
	}
	return rows
}

func toRow(exp string, sp spec, param string, seed int64, tr Trace, cfg Config, res []Result) SummaryRow {
	s := Summarize(cfg, tr, res)
	return SummaryRow{
		Experiment:        exp,
		Policy:            sp.label,
		Parameter:         param,
		Coalesce:          sp.coalesce,
		Seed:              seed,
		Replicas:          cfg.Replicas,
		Requests:          s.Requests,
		MeanTTFTMS:        s.TTFTMean,
		P50MS:             s.P50,
		P95MS:             s.P95,
		P99MS:             s.P99,
		FollowerP50MS:     s.FollowerP50,
		FollowerP99MS:     s.FollowerP99,
		PrefillMS:         s.PrefillMS,
		DuplicateRatio:    s.DuplicateRatio,
		SLOGoodput:        s.SLOGoodput,
		StreamP99MS:       s.StreamP99,
		BatchP99WaitMS:    s.BatchP99Wait,
		BatchMaxWaitMS:    s.BatchMaxWait,
		CoalescedRequests: s.CoalescedRequests,
		HoldRequests:      s.HoldRequests,
		HoldReleased:      s.HoldReleased,
	}
}

var summaryHeader = []string{
	"experiment", "policy", "parameter", "coalesce", "seed", "replicas", "requests",
	"mean_ttft_ms", "p50_ms", "p95_ms", "p99_ms", "follower_p50_ms", "follower_p99_ms",
	"prefill_ms", "duplicate_ratio", "slo_goodput", "stream_p99_ms", "batch_p99_wait_ms", "batch_max_wait_ms",
	"coalesced_requests", "hold_requests", "hold_released", "gate_action", "gate_replicas",
}

func writeSummary(path string, rows []SummaryRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write(summaryHeader); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{
			r.Experiment, r.Policy, r.Parameter, strconv.FormatBool(r.Coalesce),
			strconv.FormatInt(r.Seed, 10), strconv.Itoa(r.Replicas), strconv.Itoa(r.Requests),
			num(r.MeanTTFTMS), num(r.P50MS), num(r.P95MS), num(r.P99MS),
			num(r.FollowerP50MS), num(r.FollowerP99MS), num(r.PrefillMS),
			num(r.DuplicateRatio), num(r.SLOGoodput), num(r.StreamP99MS), num(r.BatchP99WaitMS), num(r.BatchMaxWaitMS),
			strconv.Itoa(r.CoalescedRequests), strconv.Itoa(r.HoldRequests), strconv.Itoa(r.HoldReleased),
			r.GateAction, strconv.Itoa(r.GateReplicas),
		}); err != nil {
			return err
		}
	}
	return w.Error()
}

var rawHeader = []string{
	"run", "policy", "coalesce", "id", "group", "replica", "follower",
	"arrival_ms", "start_ms", "prefill_ms", "queue_wait_ms", "ttft_ms",
	"cached_tokens", "hold", "hold_outcome",
}

func writeRaw(path string, rows []rawResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write(rawHeader); err != nil {
		return err
	}
	for _, rr := range rows {
		r := rr.res
		if err := w.Write([]string{
			rr.run, rr.policy, strconv.FormatBool(rr.coalesce), r.ID, r.Group,
			strconv.Itoa(r.Replica), strconv.FormatBool(r.Follower),
			num(r.ArrivalMS), num(r.StartMS), num(r.PrefillMS), num(r.QueueWaitMS), num(r.TTFTMS),
			strconv.Itoa(r.CachedTokens), strconv.FormatBool(r.Hold), r.HoldOutcome,
		}); err != nil {
			return err
		}
	}
	return w.Error()
}

func num(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}
