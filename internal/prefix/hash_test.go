package prefix

import (
	"strings"
	"testing"
)

func TestChainDeterministic(t *testing.T) {
	h := NewHasher("tenant-a", 8)
	p := []byte("abcdefghijklmnopqrstuvwxyz")
	a := h.Chain(p)
	b := h.Chain(p)
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("hash %d differs: %x vs %x", i, a[i], b[i])
		}
	}
	if h.Key(p) != a[len(a)-1] {
		t.Fatal("Key does not equal final chain hash")
	}
}

func TestChainTenantSeparation(t *testing.T) {
	p := []byte("the same prompt")
	a := NewHasher("tenant-a", 8).Chain(p)
	b := NewHasher("tenant-b", 8).Chain(p)
	if len(a) != len(b) || len(a) == 0 {
		t.Fatalf("unexpected chain lengths %d %d", len(a), len(b))
	}
	for i := range a {
		if a[i] == b[i] {
			t.Fatalf("tenant salts collided at block %d", i)
		}
	}
}

func TestChainPrefixStability(t *testing.T) {
	h := NewHasher("t", 8)
	base := []byte("abcdefghijklmnop")
	longer := []byte("abcdefghijklmnopXXXX")
	a := h.Chain(base)
	b := h.Chain(longer)
	if len(a) != 2 || len(b) != 3 {
		t.Fatalf("unexpected chain lengths %d %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("shared prefix block %d differs", i)
		}
	}
}

func TestChainEmpty(t *testing.T) {
	h := NewHasher("t", 8)
	if got := h.Chain(nil); got != nil {
		t.Fatalf("expected nil chain, got %v", got)
	}
	if got := h.Key(nil); got != 0 {
		t.Fatalf("expected zero key, got %d", got)
	}
}

func TestDirectoryHitAndEviction(t *testing.T) {
	d := NewDirectory(2, 128)
	chain := []uint64{1, 2, 3}
	if got := d.Record(0, chain); got != 3 {
		t.Fatalf("expected 3 inserted blocks, got %d", got)
	}
	if got := d.Size(0); got != 2 {
		t.Fatalf("expected capacity 2, got %d", got)
	}
	// Block 1 was evicted, so a chain starting there has no hit.
	if got := d.Hit(0, []uint64{1, 2}); got != 0 {
		t.Fatalf("expected zero hit after eviction, got %d", got)
	}
	// Recording 2 again refreshes it, so it survives a following insert.
	d.Record(0, []uint64{2})
	d.Record(0, []uint64{4})
	if got := d.Hit(0, []uint64{2, 4}); got != 2*128 {
		t.Fatalf("expected refreshed hit of 256 tokens, got %d", got)
	}
	if got := d.Size(1); got != 0 {
		t.Fatalf("replica 1 should be empty, got %d", got)
	}
}

func TestDirectoryHitPartialPrefix(t *testing.T) {
	d := NewDirectory(10, 100)
	d.Record(1, []uint64{10, 11, 12})
	if got := d.Hit(1, []uint64{10, 11, 99}); got != 200 {
		t.Fatalf("expected 200 tokens for two matching blocks, got %d", got)
	}
	if got := d.Hit(1, []uint64{99, 10}); got != 0 {
		t.Fatalf("expected zero for missing first block, got %d", got)
	}
}

func TestDirectoryDisabled(t *testing.T) {
	d := NewDirectory(0, 100)
	d.Record(0, []uint64{1, 2})
	if got := d.Hit(0, []uint64{1}); got != 0 {
		t.Fatalf("disabled directory should not hit, got %d", got)
	}
}

func TestAuditor(t *testing.T) {
	a := &Auditor{}
	a.Observe(100, 80)
	a.Observe(50, 70)
	s := a.Snapshot()
	if s.Samples != 2 {
		t.Fatalf("samples = %d", s.Samples)
	}
	// tp=80+50=130, fp=20, fn=20
	wantP := 130.0 / 150.0
	if diff := s.Precision - wantP; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("precision = %f, want %f", s.Precision, wantP)
	}
	if diff := s.Recall - wantP; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("recall = %f, want %f", s.Recall, wantP)
	}
}

func BenchmarkChain64KB(b *testing.B) {
	h := NewHasher("bench", 2048)
	p := []byte(strings.Repeat("x", 64*1024))
	b.SetBytes(int64(len(p)))
	b.ResetTimer()
	for b.Loop() {
		_ = h.Chain(p)
	}
}
