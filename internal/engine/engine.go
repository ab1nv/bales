// Package engine adapts Bales to OpenAI-compatible inference servers and reads
// their Prometheus metrics.
package engine

import (
	"context"
	"net/http"
)

// Outcome captures one proxied request.
type Outcome struct {
	Status           int
	FirstTokenMS     float64
	TotalMS          float64
	PromptTokens     int
	CachedTokens     int
	CompletionTokens int
}

// Metrics is the replica load view scraped from an engine.
type Metrics struct {
	Running      int
	Waiting      int
	KVCacheUsage float64 // fraction in [0,1]
	QueuedMS     float64 // optional direct queue-time metric
}

// Adapter proxies chat completions and reports replica metrics.
type Adapter interface {
	Name() string
	BaseURL() string
	Chat(ctx context.Context, body []byte, w http.ResponseWriter, onFirst func()) (Outcome, error)
	Metrics(ctx context.Context) (Metrics, error)
}
