package sim

import (
	"math"
	"testing"
)

func TestBurstCoalescingSavesPrefill(t *testing.T) {
	tr := BurstTrace(16, 2048, 32, 3)
	base := DefaultConfig()
	base.Replicas = 4
	base.PolicyName = "bales_ect"

	off := base
	off.Coalesce = false
	resOff, err := Run(off, tr)
	if err != nil {
		t.Fatal(err)
	}
	sumOff := Summarize(off, tr, resOff)

	on := base
	on.Coalesce = true
	resOn, err := Run(on, tr)
	if err != nil {
		t.Fatal(err)
	}
	sumOn := Summarize(on, tr, resOn)

	if sumOn.PrefillMS >= sumOff.PrefillMS {
		t.Fatalf("coalescing did not reduce prefill: on=%f off=%f", sumOn.PrefillMS, sumOff.PrefillMS)
	}
	if sumOn.DuplicateTokens >= sumOff.DuplicateTokens {
		t.Fatalf("coalescing did not reduce duplicate tokens: on=%d off=%d", sumOn.DuplicateTokens, sumOff.DuplicateTokens)
	}
	if sumOn.HoldReleased == 0 {
		t.Fatalf("expected released followers, summary=%+v", sumOn)
	}
}

func TestSpeculativeDirectoryCausesDuplicatePrefill(t *testing.T) {
	tr := BurstTrace(4, 2048, 8, 0)
	cfg := DefaultConfig()
	cfg.Replicas = 1
	cfg.PolicyName = "bales_ect"
	cfg.Coalesce = false
	cfg.Speculative = true
	res, err := Run(cfg, tr)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 4 {
		t.Fatalf("results = %d", len(res))
	}
	for _, r := range res[1:] {
		if r.CachedTokens != 0 {
			t.Fatalf("request %s unexpectedly used cache: %d", r.ID, r.CachedTokens)
		}
	}
	sum := Summarize(cfg, tr, res)
	if sum.DuplicateTokens != 3*2048 {
		t.Fatalf("duplicate tokens = %d, want %d", sum.DuplicateTokens, 3*2048)
	}
}

func TestSimDeterministic(t *testing.T) {
	tr := LoadTrace(8, 3000, 42)
	cfg := DefaultConfig()
	cfg.Replicas = 4
	cfg.PolicyName = "llmd_style"
	a, err := Run(cfg, tr)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Run(cfg, tr)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("result lengths differ: %d %d", len(a), len(b))
	}
	for i := range a {
		if a[i].TTFTMS != b[i].TTFTMS || a[i].Replica != b[i].Replica {
			t.Fatalf("result %d differs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestPercentile(t *testing.T) {
	v := []float64{1, 2, 3, 4, 5}
	if got := percentile(v, 50); math.Abs(got-3) > 1e-9 {
		t.Fatalf("p50 = %f", got)
	}
	if got := percentile(v, 100); got != 5 {
		t.Fatalf("p100 = %f", got)
	}
	if got := percentile(nil, 99); got != 0 {
		t.Fatalf("empty = %f", got)
	}
}

func TestTraceShapes(t *testing.T) {
	burst := BurstTrace(8, 2048, 16, 2)
	if len(burst.Requests) != 8 {
		t.Fatalf("burst requests = %d", len(burst.Requests))
	}
	if burst.Requests[0].Chain[len(burst.Requests[0].Chain)-1] != burst.Requests[7].Chain[len(burst.Requests[7].Chain)-1] {
		t.Fatal("burst prompts should share a chain")
	}
	load := LoadTrace(10, 2000, 7)
	if len(load.Requests) == 0 {
		t.Fatal("load trace is empty")
	}
	for _, r := range load.Requests {
		if r.PromptTokens <= 0 || r.OutputTokens <= 0 {
			t.Fatalf("bad request %+v", r)
		}
	}
	long := LongContextTrace(5, 1500, 3)
	if len(long.Requests) == 0 {
		t.Fatal("long trace is empty")
	}
	if long.Requests[0].PromptTokens < 8*BlockTokens {
		t.Fatalf("long prompt too short: %d", long.Requests[0].PromptTokens)
	}
}

func TestRunQueueDeterministic(t *testing.T) {
	a := RunQueue(8, 5000, 3, "edf", 5000)
	b := RunQueue(8, 5000, 3, "edf", 5000)
	if a != b {
		t.Fatalf("queue run differs: %+v vs %+v", a, b)
	}
	if a.Streams == 0 || a.Batches == 0 {
		t.Fatalf("expected both classes, got %+v", a)
	}
}
