// Package prefix implements tenant-salted chained block hashing and a
// per-replica belief directory for prefix cache state.
package prefix

import "sync"

const (
	// DefaultBlockBytes is the gateway block size. With roughly four bytes per
	// token this is close to 512 tokens, the block size used by Mooncake traces.
	DefaultBlockBytes = 2048

	offset64 = 14695981039346656037
	prime64  = 1099511628211
)

// Hasher produces chained hashes over fixed-size blocks of a prompt. The chain
// state starts from a tenant salt, so hashes from different tenants never
// collide by construction.
type Hasher struct {
	salt       uint64
	blockBytes int
}

// NewHasher returns a hasher for one tenant. A non-positive block size selects
// DefaultBlockBytes.
func NewHasher(tenant string, blockBytes int) *Hasher {
	if blockBytes <= 0 {
		blockBytes = DefaultBlockBytes
	}
	return &Hasher{
		salt:       fnv1a(offset64, []byte(tenant)),
		blockBytes: blockBytes,
	}
}

// Chain returns one hash per complete or partial block of p. An empty prompt
// produces no hashes.
func (h *Hasher) Chain(p []byte) []uint64 {
	if len(p) == 0 {
		return nil
	}
	n := (len(p) + h.blockBytes - 1) / h.blockBytes
	out := make([]uint64, 0, n)
	state := h.salt
	for off := 0; off < len(p); off += h.blockBytes {
		end := min(off+h.blockBytes, len(p))
		block := fnv1a(offset64, p[off:end])
		state = mix64(state ^ block)
		out = append(out, state)
	}
	return out
}

// Key returns the final chain hash for a prompt. It identifies the full prompt
// chain and is used as the coalescing key in the prototype.
func (h *Hasher) Key(p []byte) uint64 {
	chain := h.Chain(p)
	if len(chain) == 0 {
		return 0
	}
	return chain[len(chain)-1]
}

// BlockBytes reports the configured block size.
func (h *Hasher) BlockBytes() int { return h.blockBytes }

// SaltFingerprint returns a stable identifier for the tenant salt. It is safe
// to log; it does not reveal the tenant string.
func (h *Hasher) SaltFingerprint() uint64 { return h.salt }

func fnv1a(seed uint64, p []byte) uint64 {
	h := seed
	for _, b := range p {
		h ^= uint64(b)
		h *= prime64
	}
	return h
}

func mix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// repCache is a small LRU set of block hashes for one replica.
type repCache struct {
	order *list
	items map[uint64]*node
}

type node struct {
	hash       uint64
	prev, next *node
}

type list struct {
	head, tail *node
	n          int
}

func newRepCache() *repCache {
	return &repCache{order: &list{}, items: make(map[uint64]*node)}
}

func (c *repCache) touch(h uint64) bool {
	n, ok := c.items[h]
	if !ok {
		return false
	}
	c.order.moveToFront(n)
	return true
}

func (c *repCache) add(h uint64) bool {
	if _, ok := c.items[h]; ok {
		c.order.moveToFront(c.items[h])
		return false
	}
	n := &node{hash: h}
	c.items[h] = n
	c.order.pushFront(n)
	return true
}

func (c *repCache) evictOldest() (uint64, bool) {
	n := c.order.tail
	if n == nil {
		return 0, false
	}
	c.order.remove(n)
	delete(c.items, n.hash)
	return n.hash, true
}

func (c *repCache) remove(h uint64) bool {
	n, ok := c.items[h]
	if !ok {
		return false
	}
	c.order.remove(n)
	delete(c.items, h)
	return true
}

func (c *repCache) len() int { return len(c.items) }

func (l *list) pushFront(n *node) {
	n.prev = nil
	n.next = l.head
	if l.head != nil {
		l.head.prev = n
	}
	l.head = n
	if l.tail == nil {
		l.tail = n
	}
	l.n++
}

func (l *list) remove(n *node) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		l.head = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		l.tail = n.prev
	}
	n.prev, n.next = nil, nil
	l.n--
}

func (l *list) moveToFront(n *node) {
	if l.head == n {
		return
	}
	l.remove(n)
	l.pushFront(n)
}

// Directory is the gateway's belief about which block chains each replica has
// cached. It is updated at routing time (approximate mode) or from engine
// feedback (confirmed mode). Capacity is expressed in blocks per replica.
type Directory struct {
	mu          sync.Mutex
	capacity    int
	blockTokens int
	reps        map[int]*repCache
}

// NewDirectory creates a belief directory. capacityPerReplica of zero disables
// storage; hits always return zero in that case.
func NewDirectory(capacityPerReplica, blockTokens int) *Directory {
	if blockTokens <= 0 {
		blockTokens = 1
	}
	return &Directory{
		capacity:    capacityPerReplica,
		blockTokens: blockTokens,
		reps:        make(map[int]*repCache),
	}
}

func (d *Directory) cache(rep int) *repCache {
	c, ok := d.reps[rep]
	if !ok {
		c = newRepCache()
		d.reps[rep] = c
	}
	return c
}

// Hit returns the number of cached tokens for the longest prefix of chain
// present on rep. Matched entries are refreshed in the LRU.
func (d *Directory) Hit(rep int, chain []uint64) int {
	if len(chain) == 0 {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.reps[rep]
	if !ok {
		return 0
	}
	n := 0
	for _, h := range chain {
		if !c.touch(h) {
			break
		}
		n++
	}
	return n * d.blockTokens
}

// Record adds the chain to rep's belief and returns the number of newly added
// blocks. Old entries are evicted when capacity is exceeded.
func (d *Directory) Record(rep int, chain []uint64) int {
	if d.capacity <= 0 || len(chain) == 0 {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.cache(rep)
	added := 0
	for _, h := range chain {
		if c.add(h) {
			added++
		}
		for c.len() > d.capacity {
			if _, ok := c.evictOldest(); !ok {
				break
			}
		}
	}
	return added
}

// Forget removes the chain from rep's belief.
func (d *Directory) Forget(rep int, chain []uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.reps[rep]
	if !ok {
		return
	}
	for _, h := range chain {
		c.remove(h)
	}
}

// Size returns the number of blocks believed cached on rep.
func (d *Directory) Size(rep int) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.reps[rep]
	if !ok {
		return 0
	}
	return c.len()
}

// Capacity returns the per-replica block capacity.
func (d *Directory) Capacity() int { return d.capacity }

// Auditor compares predicted hit tokens with engine-reported cached tokens.
// Token counts, not block counts, are compared.
type Auditor struct {
	mu        sync.Mutex
	samples   int64
	predicted int64
	reported  int64
	tp        int64
	fp        int64
	fn        int64
}

// Observe records one prediction and one engine report.
func (a *Auditor) Observe(predicted, reported int) {
	if predicted < 0 {
		predicted = 0
	}
	if reported < 0 {
		reported = 0
	}
	overlap := min(predicted, reported)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.samples++
	a.predicted += int64(predicted)
	a.reported += int64(reported)
	a.tp += int64(overlap)
	if predicted > reported {
		a.fp += int64(predicted - reported)
	} else {
		a.fn += int64(reported - predicted)
	}
}

// Audit is a point-in-time view of the auditor.
type Audit struct {
	Samples   int64
	Predicted int64
	Reported  int64
	Precision float64
	Recall    float64
}

// Snapshot returns current auditor statistics. Precision and recall are 1 when
// their denominators are zero and at least one sample exists, otherwise 0.
func (a *Auditor) Snapshot() Audit {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := Audit{Samples: a.samples, Predicted: a.predicted, Reported: a.reported}
	if a.tp+a.fp > 0 {
		out.Precision = float64(a.tp) / float64(a.tp+a.fp)
	} else if a.samples > 0 {
		out.Precision = 1
	}
	if a.tp+a.fn > 0 {
		out.Recall = float64(a.tp) / float64(a.tp+a.fn)
	} else if a.samples > 0 {
		out.Recall = 1
	}
	return out
}
