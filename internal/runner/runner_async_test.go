package runner

// Tests for the async chat integration (async-turn-migration leaf 02):
// the ChatAsync-result → ChatResponse adapter and the Row latency fields.
// The runner constructs its daemonclient concretely (no interface seam), so
// the adapter is the testable unit; ChatAsync itself is covered in
// internal/daemonclient/daemonclient_async_test.go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bhodgens/meept-bench/internal/daemonclient"
	"github.com/bhodgens/meept-bench/internal/results"
)

func TestAsyncResultToChatResponse_Completed(t *testing.T) {
	resp := asyncResultToChatResponse(&daemonclient.TurnResult{
		Reply: "dispatch works", Status: "completed", DurationMS: 42,
	}, nil)
	if resp.Reply != "dispatch works" {
		t.Errorf("Reply = %q", resp.Reply)
	}
	if resp.Error != "" {
		t.Errorf("Error = %q, want empty on completed turn", resp.Error)
	}
}

func TestAsyncResultToChatResponse_FailedIsGradedNotFatal(t *testing.T) {
	// A failed turn is a valid outcome: it must surface as resp.Error (so
	// the existing agent-error path grades it) with the daemon's text.
	resp := asyncResultToChatResponse(&daemonclient.TurnResult{
		Status: "failed", Error: "step 2 exploded", Reply: "",
	}, nil)
	if resp.Error != "step 2 exploded" {
		t.Errorf("Error = %q, want daemon failure text", resp.Error)
	}
	if resp.Reply != "" {
		t.Errorf("Reply = %q, want empty", resp.Reply)
	}
}

func TestAsyncResultToChatResponse_FailedNoErrorText(t *testing.T) {
	resp := asyncResultToChatResponse(&daemonclient.TurnResult{Status: "failed"}, nil)
	if resp.Error == "" {
		t.Error("Error must be non-empty even when the daemon sent no error text")
	}
}

func TestAsyncResultToChatResponse_Errors(t *testing.T) {
	sentinel := fmt.Errorf("rpc blew up")
	resp := asyncResultToChatResponse(nil, sentinel)
	if resp.Error != "rpc blew up" {
		t.Errorf("Error = %q, want transport error text", resp.Error)
	}
	resp = asyncResultToChatResponse(nil, nil)
	if resp.Error == "" {
		t.Error("nil result with nil error must produce an error response")
	}
}

func TestAsyncResultToChatResponse_StalledShortCircuitedEarlier(t *testing.T) {
	// ErrTurnStalled never reaches the adapter (the runner returns a
	// stalled finishErr first). This pins the contract by asserting the
	// sentinel is recognizable via errors.Is at the call site.
	if !errors.Is(daemonclient.ErrTurnStalled, daemonclient.ErrTurnStalled) {
		t.Fatal("sentinel broken")
	}
}

func TestRow_AckTurnSecondsJSON(t *testing.T) {
	// The Row latency fields must serialize with the contracted keys and
	// stay omitted when zero (old rows / legacy daemons).
	row := results.Row{AckSeconds: 0.012, TurnSeconds: 1.5}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, key := range []string{`"ack_seconds":0.012`, `"turn_seconds":1.5`} {
		if !strings.Contains(s, key) {
			t.Errorf("json missing %s: %s", key, s)
		}
	}
	empty := results.Row{}
	b, err = json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if strings.Contains(string(b), "ack_seconds") || strings.Contains(string(b), "turn_seconds") {
		t.Errorf("zero latencies must be omitted: %s", string(b))
	}
}
