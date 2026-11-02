package gateway

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ab1nv/bales/internal/cost"
	"github.com/ab1nv/bales/internal/telemetry"
)

type fakeBackend struct {
	mu         sync.Mutex
	seen       map[string]int
	firstDelay time.Duration
	warmDelay  time.Duration
	tokens     int
}

func newFakeBackend(first, warm time.Duration) *fakeBackend {
	return &fakeBackend{
		seen:       make(map[string]int),
		firstDelay: first,
		warmDelay:  warm,
		tokens:     2000,
	}
}

func (f *fakeBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/metrics" {
		fmt.Fprint(w, "bales_requests_running 1\nbales_requests_waiting 0\n")
		return
	}
	var body struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)
	var prompt strings.Builder
	for _, m := range body.Messages {
		prompt.WriteString(m.Content)
	}
	key := prompt.String()
	f.mu.Lock()
	cached := f.seen[key] > 0
	f.seen[key]++
	f.mu.Unlock()

	delay := f.firstDelay
	if cached {
		delay = f.warmDelay
	}
	select {
	case <-time.After(delay):
	case <-r.Context().Done():
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	write := func(s string) {
		fmt.Fprint(w, s)
		if flusher != nil {
			flusher.Flush()
		}
	}
	write("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
	for range 3 {
		write("data: {\"choices\":[{\"delta\":{\"content\":\"tok\"}}]}\n\n")
		time.Sleep(time.Millisecond)
	}
	cachedTokens := 0
	if cached {
		cachedTokens = f.tokens
	}
	write(fmt.Sprintf("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":3,\"prompt_tokens_details\":{\"cached_tokens\":%d}}}\n\n",
		f.tokens, cachedTokens))
	write("data: [DONE]\n\n")
}

func gatewayForTest(t *testing.T, backendURL string, rec *telemetry.Recorder, mutate func(*Config)) *Server {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Replicas = []string{backendURL}
	cfg.MetricsInterval = 0
	cfg.TenantSalt = "test-salt"
	cfg.Cost = cost.DefaultModel()
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := New(cfg, rec)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func chatBody(prompt string) []byte {
	b, _ := json.Marshal(map[string]any{
		"model":  "qwen-test",
		"stream": true,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
	return b
}

func postChat(t *testing.T, url string, body []byte) (int, string) {
	t.Helper()
	resp, err := http.Post(url+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func TestGatewayRoutesAndRecords(t *testing.T) {
	backend := httptest.NewServer(newFakeBackend(5*time.Millisecond, time.Millisecond))
	defer backend.Close()
	var buf bytes.Buffer
	rec := telemetry.NewRecorder(&buf)
	s := gatewayForTest(t, backend.URL, rec, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	status, body := postChat(t, ts.URL, chatBody(strings.Repeat("a", 3993)))
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("stream not proxied: %s", body)
	}
	s.Close()

	reader := csv.NewReader(bytes.NewReader(buf.Bytes()))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("csv rows = %d, want header plus one data row: %v", len(rows), rows)
	}
	if rows[1][5] != "1000" {
		t.Fatalf("prompt_tokens = %s, want 1000", rows[1][5])
	}
	if rows[1][20] != "200" {
		t.Fatalf("status = %s, want 200", rows[1][20])
	}
}

func TestGatewayCoalescesFollowers(t *testing.T) {
	backend := httptest.NewServer(newFakeBackend(80*time.Millisecond, 5*time.Millisecond))
	defer backend.Close()
	var buf bytes.Buffer
	rec := telemetry.NewRecorder(&buf)
	s := gatewayForTest(t, backend.URL, rec, func(c *Config) {
		c.Coalesce.DeltaMaxMS = 250
		c.Coalesce.DefaultSlackMS = 2000
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := chatBody(strings.Repeat("x", 8000))
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	wg.Go(func() {
		statuses[0], _ = postChat(t, ts.URL, body)
	})
	time.Sleep(30 * time.Millisecond)
	wg.Go(func() {
		statuses[1], _ = postChat(t, ts.URL, body)
	})
	wg.Wait()
	for i, status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("request %d status = %d", i, status)
		}
	}
	s.Close()

	rows := readCSV(t, buf.Bytes())
	var released, spilled bool
	replicas := map[string]struct{}{}
	for _, row := range rows {
		replicas[row[9]] = struct{}{}
		switch row[14] {
		case "released":
			released = true
		case "spilled":
			spilled = true
		}
	}
	if !released {
		t.Fatalf("expected a released follower, rows:\n%s", formatRows(rows))
	}
	if spilled {
		t.Logf("one follower spilled; rows:\n%s", formatRows(rows))
	}
	if len(replicas) != 1 {
		t.Fatalf("requests spread across replicas: %v", replicas)
	}
}

func TestGatewayRejectsOverload(t *testing.T) {
	backend := httptest.NewServer(newFakeBackend(time.Millisecond, time.Millisecond))
	defer backend.Close()
	s := gatewayForTest(t, backend.URL, nil, func(c *Config) {
		c.QMaxMS = 1
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	status, body := postChat(t, ts.URL, chatBody(strings.Repeat("z", 4000)))
	if status != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if !strings.Contains(body, "admission") {
		t.Fatalf("body = %s", body)
	}
	s.Close()
}

func TestGatewayBadJSON(t *testing.T) {
	backend := httptest.NewServer(newFakeBackend(time.Millisecond, time.Millisecond))
	defer backend.Close()
	s := gatewayForTest(t, backend.URL, nil, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader("{"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	s.Close()
}

func TestStatsAndMetricsEndpoints(t *testing.T) {
	backend := httptest.NewServer(newFakeBackend(time.Millisecond, time.Millisecond))
	defer backend.Close()
	s := gatewayForTest(t, backend.URL, nil, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(data), "bales_queue_depth") {
		t.Fatalf("metrics body = %s", data)
	}

	resp, err = http.Get(ts.URL + "/debug/stats")
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var stats map[string]any
	if err := json.Unmarshal(data, &stats); err != nil {
		t.Fatalf("stats JSON: %v", err)
	}
	if stats["policy"] != "bales_ect" {
		t.Fatalf("stats = %v", stats)
	}
	s.Close()
}

func TestPromptTextSupportsParts(t *testing.T) {
	msgs := []message{
		{Role: "system", Content: json.RawMessage(`"be brief"`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"hello"},{"type":"image","url":"x"}]`)},
	}
	got := promptText(msgs)
	if !strings.Contains(got, "be brief") || !strings.Contains(got, "hello") {
		t.Fatalf("prompt = %q", got)
	}
}

func readCSV(t *testing.T, data []byte) [][]string {
	t.Helper()
	reader := csv.NewReader(bytes.NewReader(data))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("empty CSV")
	}
	return rows[1:]
}

func formatRows(rows [][]string) string {
	var b strings.Builder
	for _, row := range rows {
		b.WriteString(strings.Join(row, ","))
		b.WriteByte('\n')
	}
	return b.String()
}
