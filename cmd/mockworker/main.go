// Command mockworker runs a small OpenAI-compatible streaming backend with a
// calibrated prefill model and an exact-prompt cache. It lets the gateway be
// exercised end to end without a GPU.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ab1nv/bales/internal/cost"
	"github.com/ab1nv/bales/internal/prefix"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9001", "listen address")
	id := flag.String("id", "mock-1", "replica id")
	decodeMS := flag.Int("decode-ms", 2, "decode milliseconds per token")
	maxTokens := flag.Int("max-tokens", 24, "default completion length")
	scale := flag.Float64("scale", 1.0, "prefill and decode time multiplier")
	cacheCap := flag.Int("cache-entries", 256, "exact-prompt cache entries")
	flag.Parse()

	w := &worker{
		id:        *id,
		decodeMS:  *decodeMS,
		maxTokens: *maxTokens,
		scale:     *scale,
		model:     cost.DefaultModel(),
		hasher:    prefix.NewHasher("mock", 2048),
		cache:     make(map[uint64]int),
		cacheCap:  *cacheCap,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", w.chat)
	mux.HandleFunc("GET /metrics", w.metrics)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("%s listening on %s", *id, *addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

type worker struct {
	id        string
	decodeMS  int
	maxTokens int
	scale     float64
	model     cost.Model
	hasher    *prefix.Hasher

	mu         sync.Mutex
	cache      map[uint64]int
	cacheOrder []uint64
	cacheCap   int

	running   atomic.Int64
	waiting   atomic.Int64
	completed atomic.Uint64
}

type chatRequest struct {
	Model               string    `json:"model"`
	Stream              bool      `json:"stream"`
	MaxTokens           int       `json:"max_tokens"`
	MaxCompletionTokens int       `json:"max_completion_tokens"`
	Messages            []message `json:"messages"`
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

func (w *worker) chat(rw http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 4<<20)).Decode(&req); err != nil {
		http.Error(rw, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	var b strings.Builder
	for _, m := range req.Messages {
		if t := m.text(); t != "" {
			b.WriteString(m.Role)
			b.WriteString(": ")
			b.WriteString(t)
			b.WriteString("\n")
		}
	}
	prompt := b.String()
	if prompt == "" {
		prompt = " "
	}
	tokens := max(1, int(math.Ceil(float64(len(prompt))*0.25)))
	chain := w.hasher.Chain([]byte(prompt))
	var key uint64
	if len(chain) > 0 {
		key = chain[len(chain)-1]
	}
	w.mu.Lock()
	cached := min(w.cache[key], tokens)
	w.mu.Unlock()

	n := req.MaxCompletionTokens
	if n <= 0 {
		n = req.MaxTokens
	}
	if n <= 0 {
		n = w.maxTokens
	}
	n = min(max(n, 1), 256)

	w.running.Add(1)
	defer w.running.Add(-1)
	w.waiting.Add(1)
	prefill := w.model.Cost(tokens-cached, cached) * w.scale
	if prefill < 1 {
		prefill = 1
	}
	if !sleep(r.Context(), time.Duration(prefill*float64(time.Millisecond))) {
		w.waiting.Add(-1)
		return
	}
	w.waiting.Add(-1)

	w.mu.Lock()
	if cached == 0 {
		if len(w.cache) >= w.cacheCap {
			w.cache = make(map[uint64]int)
			w.cacheOrder = w.cacheOrder[:0]
		}
		w.cache[key] = tokens
		w.cacheOrder = append(w.cacheOrder, key)
	}
	w.mu.Unlock()

	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	flusher, _ := rw.(http.Flusher)
	send := func(format string, args ...any) bool {
		if _, err := fmt.Fprintf(rw, format, args...); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	if !send("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n") {
		return
	}
	for i := range n {
		if !sleep(r.Context(), time.Duration(w.decodeMS)*time.Millisecond) {
			return
		}
		if !send("data: {\"choices\":[{\"delta\":{\"content\":\"tok%d \"}}]}\n\n", i) {
			return
		}
	}
	if !send("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d,\"prompt_tokens_details\":{\"cached_tokens\":%d}}}\n\n",
		tokens, n, cached) {
		return
	}
	send("data: [DONE]\n\n")
	w.completed.Add(1)
}

func (w *worker) metrics(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	cacheLen := len(w.cache)
	w.mu.Unlock()
	usage := 0.0
	if w.cacheCap > 0 {
		usage = float64(cacheLen) / float64(w.cacheCap)
	}
	rw.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(rw, "bales_requests_running %d\n", w.running.Load())
	fmt.Fprintf(rw, "bales_requests_waiting %d\n", w.waiting.Load())
	fmt.Fprintf(rw, "bales_kv_cache_usage %f\n", usage)
	fmt.Fprintf(rw, "bales_queued_prefill_ms 0\n")
	fmt.Fprintf(rw, "bales_completed_total %d\n", w.completed.Load())
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
