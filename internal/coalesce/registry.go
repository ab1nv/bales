package coalesce

import (
	"sync"
	"time"
)

// Outcome describes how an in-flight leader finished.
type Outcome uint8

const (
	// Ready means the leader produced its first token and the cache is warm.
	Ready Outcome = iota
	// Failed means the leader errored or disconnected.
	Failed
)

// Group tracks one in-flight leader and the followers that may wait on it.
type Group struct {
	Key      uint64
	Replica  int
	Started  time.Time
	EstDone  time.Time
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	outcome  Outcome
	finished bool
	follows  int
}

// Done is closed when the leader finishes, successfully or not.
func (g *Group) Done() <-chan struct{} { return g.done }

// Outcome returns the leader outcome after Done is closed.
func (g *Group) Outcome() Outcome {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.outcome
}

// Followers returns the number of requests that joined this group.
func (g *Group) Followers() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.follows
}

func (g *Group) addFollower() {
	g.mu.Lock()
	g.follows++
	g.mu.Unlock()
}

func (g *Group) finish(o Outcome) {
	g.mu.Lock()
	if g.finished {
		g.mu.Unlock()
		return
	}
	g.finished = true
	g.outcome = o
	g.mu.Unlock()
	g.once.Do(func() { close(g.done) })
}

// Registry maps a chain hash to its current in-flight leader. At most one
// leader exists per chain and replica set, which is what lets followers decide
// between waiting and spilling.
type Registry struct {
	mu     sync.Mutex
	groups map[uint64]*Group
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{groups: make(map[uint64]*Group)}
}

// Start registers key as in flight on replica. If a leader already exists it is
// returned with fresh=false and a follower is counted.
func (r *Registry) Start(key uint64, replica int, now time.Time, est time.Duration) (*Group, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.groups[key]; ok {
		g.addFollower()
		return g, false
	}
	g := &Group{
		Key:     key,
		Replica: replica,
		Started: now,
		EstDone: now.Add(est),
		done:    make(chan struct{}),
	}
	r.groups[key] = g
	return g, true
}

// Get returns the current leader for key, if any.
func (r *Registry) Get(key uint64) *Group {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.groups[key]
}

// Complete finishes g and removes it if it is still the registered leader.
func (r *Registry) Complete(g *Group, outcome Outcome) {
	if g == nil {
		return
	}
	r.mu.Lock()
	if cur, ok := r.groups[g.Key]; ok && cur == g {
		delete(r.groups, g.Key)
	}
	r.mu.Unlock()
	g.finish(outcome)
}

// Len returns the number of in-flight groups.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.groups)
}
