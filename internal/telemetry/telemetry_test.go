package telemetry

import (
	"bytes"
	"encoding/csv"
	"testing"
	"time"
)

func TestRecorderWritesContract(t *testing.T) {
	var buf bytes.Buffer
	r := NewRecorder(&buf)
	err := r.Record(Row{
		Time:             time.Unix(1_700_000_000, 0).UTC(),
		RequestID:        "req-1",
		Tenant:           "t",
		Class:            "stream",
		Model:            "m",
		PromptTokens:     100,
		PredictedCached:  80,
		EngineCached:     80,
		GroupID:          "abc",
		Replica:          "0",
		Policy:           "bales_ect",
		PolicyReason:     "lowest ECT",
		Coalesced:        true,
		HoldMS:           12.5,
		HoldOutcome:      "released",
		QueueWaitMS:      1,
		PrefillEstMS:     2,
		TTFTMS:           3,
		TotalMS:          4,
		CompletionTokens: 5,
		Status:           200,
		Stream:           true,
		SlackMS:          2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if got := r.Rows(); got != 1 {
		t.Fatalf("rows = %d", got)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("csv lines = %d", len(rows))
	}
	if len(rows[0]) != len(Header) || len(rows[1]) != len(Header) {
		t.Fatalf("column count header=%d row=%d", len(rows[0]), len(rows[1]))
	}
	if rows[1][1] != "req-1" || rows[1][12] != "true" || rows[1][14] != "released" {
		t.Fatalf("row = %v", rows[1])
	}
}
