package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatStreamsAndMeasures(t *testing.T) {
	const stream = `data: {"choices":[{"delta":{"role":"assistant"}}]}

data: {"choices":[{"delta":{"content":"Hel"}}]}

data: {"choices":[{"delta":{"content":"lo"}}]}

data: {"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":100}}}

data: [DONE]
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for line := range strings.SplitSeq(stream, "\n") {
			fmt.Fprintln(w, line)
		}
	}))
	defer srv.Close()

	a := NewOpenAI("mock", srv.URL)
	rec := httptest.NewRecorder()
	first := false
	out, err := a.Chat(context.Background(), []byte(`{"stream":true}`), rec, func() { first = true })
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Fatal("first-token callback was not called")
	}
	if out.Status != http.StatusOK {
		t.Fatalf("status = %d", out.Status)
	}
	if out.PromptTokens != 120 || out.CachedTokens != 100 || out.CompletionTokens != 2 {
		t.Fatalf("outcome = %+v", out)
	}
	if !strings.Contains(rec.Body.String(), "Hel") || !strings.Contains(rec.Body.String(), "[DONE]") {
		t.Fatalf("response body not proxied: %q", rec.Body.String())
	}
	if out.TotalMS <= 0 {
		t.Fatalf("total ms = %f", out.TotalMS)
	}
}

func TestChatUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	a := NewOpenAI("mock", srv.URL)
	rec := httptest.NewRecorder()
	out, err := a.Chat(context.Background(), []byte(`{}`), rec, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if out.Status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", out.Status)
	}
	if !strings.Contains(rec.Body.String(), "overloaded") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestChatNonStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":1}}`)
	}))
	defer srv.Close()
	a := NewOpenAI("mock", srv.URL)
	rec := httptest.NewRecorder()
	out, err := a.Chat(context.Background(), []byte(`{}`), rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.PromptTokens != 10 || out.CompletionTokens != 1 {
		t.Fatalf("outcome = %+v", out)
	}
	if !strings.Contains(rec.Body.String(), `"content":"hi"`) {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestParseMetrics(t *testing.T) {
	text := `# HELP vllm:num_requests_running running
vllm:num_requests_running{model_name="qwen"} 3
vllm:num_requests_waiting 2
vllm:gpu_cache_usage_perc 0.42
bales_queued_prefill_ms 17.5
`
	m, err := ParseMetrics(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	if m.Running != 3 || m.Waiting != 2 {
		t.Fatalf("metrics = %+v", m)
	}
	if m.KVCacheUsage != 0.42 || m.QueuedMS != 17.5 {
		t.Fatalf("metrics = %+v", m)
	}
}

func TestMetricsScrape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "bales_requests_running 5\nbales_requests_waiting 1\n")
	}))
	defer srv.Close()
	a := NewOpenAI("mock", srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m, err := a.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m.Running != 5 || m.Waiting != 1 {
		t.Fatalf("metrics = %+v", m)
	}
}
