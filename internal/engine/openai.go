package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OpenAI adapts an OpenAI-compatible chat completions server. Streaming is
// proxied line by line so TTFT and usage can be measured without buffering the
// whole response.
type OpenAI struct {
	ID     string
	URL    string
	APIKey string
	Client *http.Client
}

// NewOpenAI builds an adapter for baseURL.
func NewOpenAI(id, baseURL string) *OpenAI {
	return &OpenAI{ID: id, URL: strings.TrimRight(baseURL, "/")}
}

func (a *OpenAI) Name() string    { return a.ID }
func (a *OpenAI) BaseURL() string { return a.URL }

func (a *OpenAI) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return http.DefaultClient
}

// Chat proxies a streaming chat completion.
func (a *OpenAI) Chat(ctx context.Context, body []byte, w http.ResponseWriter, onFirst func()) (Outcome, error) {
	start := time.Now()
	out := Outcome{}
	url := a.URL + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return out, fmt.Errorf("engine %s: build request: %w", a.ID, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if a.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.APIKey)
	}
	resp, err := a.client().Do(req)
	if err != nil {
		out.TotalMS = since(start)
		return out, fmt.Errorf("engine %s: request: %w", a.ID, err)
	}
	defer resp.Body.Close()
	out.Status = resp.StatusCode

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		_, _ = w.Write(data)
		flush()
		out.TotalMS = since(start)
		return out, fmt.Errorf("engine %s: upstream status %d", a.ID, resp.StatusCode)
	}

	var once sync.Once
	mark := func() {
		once.Do(func() {
			out.FirstTokenMS = since(start)
			if onFirst != nil {
				onFirst()
			}
		})
	}

	if strings.Contains(ct, "text/event-stream") {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Bytes()
			if _, err := w.Write(line); err != nil {
				out.TotalMS = since(start)
				return out, fmt.Errorf("engine %s: write: %w", a.ID, err)
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				out.TotalMS = since(start)
				return out, fmt.Errorf("engine %s: write: %w", a.ID, err)
			}
			flush()
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			payload := bytes.TrimSpace(line[len("data:"):])
			if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
				continue
			}
			var chunk sseChunk
			if err := json.Unmarshal(payload, &chunk); err != nil {
				continue
			}
			mark()
			if chunk.Usage != nil {
				applyUsage(&out, chunk.Usage)
			} else {
				for _, c := range chunk.Choices {
					if c.Delta.Content != "" {
						out.CompletionTokens++
					}
				}
			}
		}
		if err := sc.Err(); err != nil {
			out.TotalMS = since(start)
			return out, fmt.Errorf("engine %s: read stream: %w", a.ID, err)
		}
	} else {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			out.TotalMS = since(start)
			return out, fmt.Errorf("engine %s: read body: %w", a.ID, err)
		}
		mark()
		_, _ = w.Write(data)
		flush()
		var completion nonStream
		if err := json.Unmarshal(data, &completion); err == nil {
			if completion.Usage != nil {
				applyUsage(&out, completion.Usage)
			}
		}
	}

	out.TotalMS = since(start)
	return out, nil
}

// Metrics scrapes Prometheus text from the engine.
func (a *OpenAI) Metrics(ctx context.Context) (Metrics, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL+"/metrics", nil)
	if err != nil {
		return Metrics{}, fmt.Errorf("engine %s: metrics request: %w", a.ID, err)
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return Metrics{}, fmt.Errorf("engine %s: metrics: %w", a.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Metrics{}, fmt.Errorf("engine %s: metrics status %d", a.ID, resp.StatusCode)
	}
	return ParseMetrics(resp.Body)
}

// ParseMetrics reads the subset of Prometheus metrics used for routing.
func ParseMetrics(r io.Reader) (Metrics, error) {
	var m Metrics
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 16*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.ReplaceAll(strings.SplitN(fields[0], "{", 2)[0], ":", "_")
		value, err := parseFloat(fields[len(fields)-1])
		if err != nil {
			continue
		}
		switch name {
		case "vllm_num_requests_running", "bales_requests_running", "bales_num_requests_running":
			m.Running = int(value)
		case "vllm_num_requests_waiting", "bales_requests_waiting", "bales_num_requests_waiting":
			m.Waiting = int(value)
		case "vllm_gpu_cache_usage_perc", "vllm_kv_cache_usage_perc", "bales_kv_cache_usage":
			m.KVCacheUsage = value
		case "bales_queued_prefill_ms", "vllm_queued_prefill_ms":
			m.QueuedMS = value
		}
	}
	if err := sc.Err(); err != nil {
		return m, fmt.Errorf("metrics: scan: %w", err)
	}
	return m, nil
}

func applyUsage(out *Outcome, u *usage) {
	if u == nil {
		return
	}
	out.PromptTokens = u.PromptTokens
	out.CompletionTokens = u.CompletionTokens
	cached := max(u.PromptTokensDetails.CachedTokens, u.PromptCacheHitTokens)
	out.CachedTokens = cached
}

func since(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000
}

func parseFloat(s string) (float64, error) {
	var v float64
	_, err := fmt.Sscanf(s, "%f", &v)
	return v, err
}

type usage struct {
	PromptTokens         int `json:"prompt_tokens"`
	CompletionTokens     int `json:"completion_tokens"`
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails  struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type sseChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
}

type nonStream struct {
	Usage *usage `json:"usage"`
}
