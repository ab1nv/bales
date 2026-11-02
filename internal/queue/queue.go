// Package queue implements a virtual-deadline admission queue. Deadlines are
// static at arrival, so ordering is independent of the current time and an
// insert costs O(log n).
package queue

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Class separates latency-sensitive streams from batch work.
type Class uint8

const (
	Stream Class = iota
	Batch
)

func (c Class) String() string {
	if c == Batch {
		return "batch"
	}
	return "stream"
}

// Request is the queue-visible part of an inference request.
type Request struct {
	ID        string
	Arrival   time.Time
	Class     Class
	EstCostMS float64
	Slack     time.Duration
	Meta      any // caller payload, opaque to the queue
}

// Config controls deadline offsets and admission.
type Config struct {
	DStream time.Duration // base deadline for streams, for example 300ms
	DBatch  time.Duration // base deadline for batch jobs, for example 5s
	Eta     float64       // weight on estimated cost in the deadline
	QMaxMS  float64       // cap on queued estimated work, in milliseconds
	Now     func() time.Time
}

func (c Config) normalized() Config {
	if c.DStream <= 0 {
		c.DStream = 300 * time.Millisecond
	}
	if c.DBatch <= 0 {
		c.DBatch = 5 * time.Second
	}
	if c.Eta < 0 {
		c.Eta = 0
	}
	if c.QMaxMS <= 0 {
		c.QMaxMS = 30000
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// ErrOverloaded is returned when admission would exceed QMaxMS.
var ErrOverloaded = errors.New("queue: admission limit reached")

// ErrInvalidRequest is returned for requests without an ID.
var ErrInvalidRequest = errors.New("queue: request ID is required")

// Item is a queued request with its static deadline.
type Item struct {
	Req      Request
	Deadline time.Time
	index    int
}

// Queue is safe for concurrent producers and consumers.
type Queue struct {
	mu       sync.Mutex
	cfg      Config
	items    itemHeap
	queuedMS float64
	admitted uint64
	rejected uint64
	wake     chan struct{}
}

// New creates a queue with the supplied configuration.
func New(cfg Config) *Queue {
	q := &Queue{cfg: cfg.normalized(), wake: make(chan struct{}, 1)}
	heap.Init(&q.items)
	return q
}

// DeadlineFor computes the static virtual deadline for r.
func (q *Queue) DeadlineFor(r Request) time.Time {
	base := q.cfg.DStream
	if r.Class == Batch {
		base = q.cfg.DBatch
	}
	off := base + time.Duration(q.cfg.Eta*r.EstCostMS*float64(time.Millisecond))
	return r.Arrival.Add(off)
}

// Push admits r or returns ErrOverloaded. Duplicate IDs are not checked; callers
// own ID uniqueness.
func (q *Queue) Push(r Request) (*Item, error) {
	if r.ID == "" {
		return nil, ErrInvalidRequest
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if r.EstCostMS > 0 && q.queuedMS+r.EstCostMS > q.cfg.QMaxMS {
		q.rejected++
		return nil, ErrOverloaded
	}
	it := &Item{Req: r, Deadline: q.DeadlineFor(r)}
	heap.Push(&q.items, it)
	if r.EstCostMS > 0 {
		q.queuedMS += r.EstCostMS
	}
	q.admitted++
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return it, nil
}

// Pop removes and returns the earliest-deadline item, or nil when empty.
func (q *Queue) Pop() *Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}
	it := heap.Pop(&q.items).(*Item)
	if it.Req.EstCostMS > 0 {
		q.queuedMS -= it.Req.EstCostMS
		if q.queuedMS < 0 {
			q.queuedMS = 0
		}
	}
	return it
}

// PopWait blocks until an item is available or ctx ends.
func (q *Queue) PopWait(ctx context.Context) (*Item, bool) {
	for {
		if it := q.Pop(); it != nil {
			return it, true
		}
		select {
		case <-q.wake:
		case <-ctx.Done():
			return nil, false
		}
	}
}

// Len returns the number of queued items.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// QueuedMS returns the estimated queued work in milliseconds.
func (q *Queue) QueuedMS() float64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.queuedMS
}

// Stats is a snapshot of admission counters.
type Stats struct {
	Queued   int
	QueuedMS float64
	Admitted uint64
	Rejected uint64
}

// Stats returns current counters.
func (q *Queue) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return Stats{
		Queued:   len(q.items),
		QueuedMS: q.queuedMS,
		Admitted: q.admitted,
		Rejected: q.rejected,
	}
}

type itemHeap []*Item

func (h itemHeap) Len() int { return len(h) }

func (h itemHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if !a.Deadline.Equal(b.Deadline) {
		return a.Deadline.Before(b.Deadline)
	}
	if !a.Req.Arrival.Equal(b.Req.Arrival) {
		return a.Req.Arrival.Before(b.Req.Arrival)
	}
	return a.Req.ID < b.Req.ID
}

func (h itemHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *itemHeap) Push(x any) {
	it := x.(*Item)
	it.index = len(*h)
	*h = append(*h, it)
}

func (h *itemHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	it.index = -1
	*h = old[:n-1]
	return it
}

func (it *Item) String() string {
	return fmt.Sprintf("%s/%s d=%s", it.Req.ID, it.Req.Class, it.Deadline.Format(time.RFC3339Nano))
}
