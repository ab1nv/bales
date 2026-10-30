package coalesce

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ab1nv/bales/internal/cost"
)

func testModel() cost.Model {
	return cost.Model{A: 6.7e-6, B: 6.7e-6, F: 0.05, G: 5}
}

func testClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestRegistryLifecycle(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1_700_000_000, 0)
	g, fresh := reg.Start(42, 1, now, 100*time.Millisecond)
	if !fresh {
		t.Fatal("first start should be fresh")
	}
	g2, fresh := reg.Start(42, 1, now, 100*time.Millisecond)
	if fresh || g2 != g {
		t.Fatal("second start should join the existing leader")
	}
	if g.Followers() != 1 {
		t.Fatalf("followers = %d, want 1", g.Followers())
	}
	reg.Complete(g, Ready)
	select {
	case <-g.Done():
	default:
		t.Fatal("Done should be closed")
	}
	if g.Outcome() != Ready {
		t.Fatalf("outcome = %v, want Ready", g.Outcome())
	}
	if reg.Len() != 0 {
		t.Fatal("registry should be empty")
	}
	g3, fresh := reg.Start(42, 1, now, time.Second)
	if !fresh || g3 == g {
		t.Fatal("a completed key should start a new group")
	}
	reg.Complete(g3, Failed)
	if g3.Outcome() != Failed {
		t.Fatal("failed outcome not recorded")
	}
	reg.Complete(g3, Failed) // double complete must not panic
}

func TestRegistryConcurrent(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1_700_000_000, 0)
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			g, _ := reg.Start(7, 0, now, time.Millisecond)
			reg.Complete(g, Ready)
		})
	}
	wg.Wait()
	if reg.Len() != 0 {
		t.Fatalf("registry length = %d", reg.Len())
	}
}

func TestDecideDispatchWithoutLeader(t *testing.T) {
	c := New(testModel(), Config{Now: testClock(time.Unix(1_700_000_000, 0))})
	d := c.Decide(Arrival{Key: 1, QTokens: 100, SharedTokens: 1000})
	if d.Action != Dispatch {
		t.Fatalf("action = %v, want dispatch", d.Action)
	}
}

func TestDecideHoldOrSpill(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	model := testModel()
	cfg := Config{
		EpsilonMS:      1,
		DeltaMaxMS:     250,
		DefaultSlackMS: 1000,
		Now:            testClock(base),
	}
	const key = 99
	const q = 200
	saving := model.Saving(q, 4096)

	cases := []struct {
		name         string
		est          time.Duration
		sharedTokens int
		waitLeaderMS float64
		waitBestMS   float64
		slackMS      float64
		deltaCapMS   float64
		want         Action
		wantDeadline time.Duration
		reasonPart   string
	}{
		{
			name: "leader just started, spill",
			est:  100 * time.Millisecond, sharedTokens: 4096, waitLeaderMS: 300,
			want: Spill, reasonPart: "cheaper",
		},
		{
			name: "leader nearly done, hold",
			est:  60 * time.Millisecond, sharedTokens: 4096, waitLeaderMS: 10,
			want: Hold, wantDeadline: 90 * time.Millisecond, reasonPart: "saves",
		},
		{
			name: "remaining prefill exceeds saving, spill",
			est:  150 * time.Millisecond, sharedTokens: 2048,
			want: Spill, reasonPart: "cheaper",
		},
		{
			name: "slack too small",
			est:  100 * time.Millisecond, sharedTokens: 4096, slackMS: 50,
			want: Spill, reasonPart: "slack",
		},
		{
			name: "hold capped by delta",
			est:  300 * time.Millisecond, sharedTokens: 16384, deltaCapMS: 40,
			want: Spill, reasonPart: "cap",
		},
		{
			name: "hold deadline capped by slack",
			est:  100 * time.Millisecond, sharedTokens: 4096, slackMS: 120,
			want: Hold, wantDeadline: 120 * time.Millisecond,
		},
		{
			name: "wait on leader erases gain",
			est:  0, sharedTokens: 4096, waitLeaderMS: 400,
			want: Spill, reasonPart: "cheaper",
		},
		{
			name: "alternative queue is longer, hold",
			est:  0, sharedTokens: 4096, waitBestMS: 400,
			want: Hold, reasonPart: "saves",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(model, cfg)
			if tc.deltaCapMS > 0 {
				c.cfg.DeltaMaxMS = tc.deltaCapMS
			}
			c.reg.Start(key, 0, base, tc.est)
			d := c.Decide(Arrival{
				Key:          key,
				QTokens:      q,
				SharedTokens: tc.sharedTokens,
				WaitLeaderMS: tc.waitLeaderMS,
				WaitBestMS:   tc.waitBestMS,
				SlackMS:      tc.slackMS,
			})
			if d.Action != tc.want {
				t.Fatalf("action = %v (%s), want %v; saving=%f", d.Action, d.Reason, tc.want, saving)
			}
			if tc.wantDeadline > 0 && d.Deadline != tc.wantDeadline {
				t.Fatalf("deadline = %v, want %v", d.Deadline, tc.wantDeadline)
			}
			if tc.reasonPart != "" && !strings.Contains(d.Reason, tc.reasonPart) {
				t.Fatalf("reason = %q, want substring %q", d.Reason, tc.reasonPart)
			}
		})
	}
}

func TestDecideNoSharedPrefix(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	c := New(testModel(), Config{Now: testClock(base)})
	c.reg.Start(5, 0, base, 10*time.Millisecond)
	d := c.Decide(Arrival{Key: 5, QTokens: 100, SharedTokens: 0})
	if d.Action != Spill || !strings.Contains(d.Reason, "shared") {
		t.Fatalf("decision = %+v", d)
	}
}
