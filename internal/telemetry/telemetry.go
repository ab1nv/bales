// Package telemetry writes one CSV row per request. The column set is the
// audit contract used by the simulator, benchmarks and report figures.
package telemetry

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// Row is one request observation.
type Row struct {
	Time             time.Time
	RequestID        string
	Tenant           string
	Class            string
	Model            string
	PromptTokens     int
	PredictedCached  int
	EngineCached     int
	GroupID          string
	Replica          string
	Policy           string
	PolicyReason     string
	Coalesced        bool
	HoldMS           float64
	HoldOutcome      string
	QueueWaitMS      float64
	PrefillEstMS     float64
	TTFTMS           float64
	TotalMS          float64
	CompletionTokens int
	Status           int
	Stream           bool
	SlackMS          float64
	Err              string
}

// Header is the fixed CSV header.
var Header = []string{
	"ts", "request_id", "tenant", "class", "model", "prompt_tokens",
	"predicted_cached_tokens", "engine_cached_tokens", "group_id", "replica",
	"policy", "policy_reason", "coalesced", "hold_ms", "hold_outcome",
	"queue_wait_ms", "prefill_est_ms", "ttft_ms", "total_ms",
	"completion_tokens", "status", "stream", "slack_ms", "error",
}

// Recorder serialises rows to a CSV writer.
type Recorder struct {
	mu     sync.Mutex
	w      *csv.Writer
	rows   int64
	closed bool
}

// NewRecorder writes the header immediately. A nil writer is allowed and
// creates a recorder that discards rows.
func NewRecorder(w io.Writer) *Recorder {
	var cw *csv.Writer
	if w != nil {
		cw = csv.NewWriter(w)
		_ = cw.Write(Header)
		cw.Flush()
	}
	return &Recorder{w: cw}
}

// Record appends one row and flushes so tailing works during a run.
func (r *Recorder) Record(row Row) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.w == nil || r.closed {
		return nil
	}
	rec := []string{
		row.Time.Format(time.RFC3339Nano),
		row.RequestID,
		row.Tenant,
		row.Class,
		row.Model,
		strconv.Itoa(row.PromptTokens),
		strconv.Itoa(row.PredictedCached),
		strconv.Itoa(row.EngineCached),
		row.GroupID,
		row.Replica,
		row.Policy,
		row.PolicyReason,
		strconv.FormatBool(row.Coalesced),
		fmtFloat(row.HoldMS),
		row.HoldOutcome,
		fmtFloat(row.QueueWaitMS),
		fmtFloat(row.PrefillEstMS),
		fmtFloat(row.TTFTMS),
		fmtFloat(row.TotalMS),
		strconv.Itoa(row.CompletionTokens),
		strconv.Itoa(row.Status),
		strconv.FormatBool(row.Stream),
		fmtFloat(row.SlackMS),
		row.Err,
	}
	if err := r.w.Write(rec); err != nil {
		return fmt.Errorf("telemetry: write: %w", err)
	}
	r.w.Flush()
	if err := r.w.Error(); err != nil {
		return fmt.Errorf("telemetry: flush: %w", err)
	}
	r.rows++
	return nil
}

// Rows returns the number of written data rows.
func (r *Recorder) Rows() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rows
}

// Close flushes and stops the recorder.
func (r *Recorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	if r.w != nil {
		r.w.Flush()
	}
}

func fmtFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}
