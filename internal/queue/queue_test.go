package queue

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

func baseTime() time.Time { return time.Unix(1_700_000_000, 0) }

func ms(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }

func TestStaticDeadlineOrder(t *testing.T) {
	base := baseTime()
	q := New(Config{DStream: 300 * time.Millisecond, DBatch: 5 * time.Second, Eta: 1})
	reqs := []Request{
		{ID: "stream-early", Arrival: base, Class: Stream, EstCostMS: 100},
		{ID: "batch", Arrival: base, Class: Batch, EstCostMS: 50},
		{ID: "stream-late", Arrival: base.Add(100 * time.Millisecond), Class: Stream, EstCostMS: 10},
	}
	// Insert out of order.
	for _, i := range []int{2, 0, 1} {
		if _, err := q.Push(reqs[i]); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"stream-early", "stream-late", "batch"}
	for _, id := range want {
		it := q.Pop()
		if it == nil {
			t.Fatalf("queue empty, want %s", id)
		}
		if it.Req.ID != id {
			t.Fatalf("popped %s, want %s", it.Req.ID, id)
		}
	}
	if q.Pop() != nil {
		t.Fatal("expected empty queue")
	}
}

func TestAdmissionLimit(t *testing.T) {
	q := New(Config{QMaxMS: 100})
	base := baseTime()
	if _, err := q.Push(Request{ID: "a", Arrival: base, EstCostMS: 60}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Push(Request{ID: "b", Arrival: base, EstCostMS: 60}); err != ErrOverloaded {
		t.Fatalf("second push error = %v, want ErrOverloaded", err)
	}
	q.Pop()
	if _, err := q.Push(Request{ID: "c", Arrival: base, EstCostMS: 60}); err != nil {
		t.Fatalf("push after pop: %v", err)
	}
	st := q.Stats()
	if st.Rejected != 1 || st.Admitted != 2 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestInvalidRequest(t *testing.T) {
	q := New(Config{})
	if _, err := q.Push(Request{}); err != ErrInvalidRequest {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
}

func TestPopWaitCancel(t *testing.T) {
	q := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if it, ok := q.PopWait(ctx); ok || it != nil {
		t.Fatal("expected cancellation with no item")
	}
}

func TestPopWaitWakes(t *testing.T) {
	q := New(Config{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		_, _ = q.Push(Request{ID: "wake", Arrival: time.Now()})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	it, ok := q.PopWait(ctx)
	if !ok || it.Req.ID != "wake" {
		t.Fatalf("PopWait = %v %v", it, ok)
	}
}

// TestWaitBoundFuzz checks the Proposition 3 bound on an EDF server with
// admission. Capacity is one millisecond of work per millisecond; the
// non-preemptive allowance is the largest single job.
func TestWaitBoundFuzz(t *testing.T) {
	const (
		trials = 200
		perRun = 120
	)
	for trial := range trials {
		rng := rand.New(rand.NewSource(int64(trial) + 1))
		base := baseTime()
		const dStreamMS = 60.0
		cfg := Config{
			DStream: ms(dStreamMS),
			DBatch:  ms(600),
			Eta:     0.3,
			QMaxMS:  250,
		}
		q := New(cfg)

		type job struct {
			id        string
			arrival   float64
			work      float64
			class     Class
			deadline  time.Time
			offsetMS  float64
			admitted  bool
			queuedAtA float64
			start     float64
		}
		jobs := make([]*job, perRun)
		arrival := 0.0
		maxWork := 0.0
		for i := range jobs {
			arrival += rng.Float64() * 25
			work := 1 + rng.Float64()*40
			if work > maxWork {
				maxWork = work
			}
			class := Stream
			if rng.Float64() < 0.25 {
				class = Batch
			}
			jobs[i] = &job{
				id:      fmt.Sprintf("j%d", i),
				arrival: arrival,
				work:    work,
				class:   class,
			}
		}

		// Work-conserving EDF server with non-preemptive jobs. The queue is the
		// source of truth for both admission and service order.
		var pending []*job
		serverFree := math.Inf(-1)
		startPending := func(now float64) {
			for len(pending) > 0 && serverFree <= now+1e-9 {
				it := q.Pop()
				if it == nil {
					t.Fatalf("queue empty while pending jobs remain")
				}
				idx := -1
				for k := range pending {
					if pending[k].id == it.Req.ID {
						idx = k
						break
					}
				}
				if idx < 0 {
					t.Fatalf("popped unknown job %s", it.Req.ID)
				}
				j := pending[idx]
				pending = append(pending[:idx], pending[idx+1:]...)
				start := math.Max(serverFree, j.arrival)
				j.start = start
				serverFree = start + j.work
			}
		}
		for _, j := range jobs {
			startPending(j.arrival)
			it, err := q.Push(Request{
				ID:        j.id,
				Arrival:   base.Add(ms(j.arrival)),
				Class:     j.class,
				EstCostMS: j.work,
			})
			if err == ErrOverloaded {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			j.admitted = true
			j.deadline = it.Deadline
			j.offsetMS = it.Deadline.Sub(base.Add(ms(j.arrival))).Seconds() * 1000
			j.queuedAtA = q.QueuedMS()
			pending = append(pending, j)
			startPending(j.arrival)
		}
		startPending(math.Inf(1))

		for _, i := range jobs {
			if !i.admitted {
				continue
			}
			delta := i.offsetMS - dStreamMS
			aheadArrivals := 0.0
			if delta > 0 {
				for _, j := range jobs {
					if !j.admitted || j == i {
						continue
					}
					if j.arrival > i.arrival && j.arrival <= i.arrival+delta && j.deadline.Before(i.deadline) {
						aheadArrivals += j.work
					}
				}
			}
			bound := (i.queuedAtA + aheadArrivals) + maxWork
			wait := i.start - i.arrival
			if wait < -1e-9 {
				t.Fatalf("trial %d job %s started before arrival: wait=%f", trial, i.id, wait)
			}
			if wait > bound+1e-6 {
				t.Fatalf("trial %d job %s violates bound: wait=%.3f bound=%.3f queued=%.3f arrivals=%.3f smax=%.3f",
					trial, i.id, wait, bound, i.queuedAtA, aheadArrivals, maxWork)
			}
		}
	}
}
