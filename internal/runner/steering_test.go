package runner

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bhodgens/meept-bench/internal/daemonclient"
	"github.com/bhodgens/meept-bench/internal/results"
	"github.com/bhodgens/meept-bench/internal/suite"
)

// deadSocket is a socket path no daemon listens on, so every turn delivery
// fails fast with a dial error — hermetic, no side effects on a live daemon.
func deadSocket(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "no-such.sock")
}

// TestScheduleTurnsDeadSocket verifies the scheduler terminates and records
// exactly one TurnEvent per scheduled turn even when every delivery path
// fails (dial error to a dead socket: not classified as queue-gone, retried
// once per path, then recorded as a failure). A cancelled context drains the
// delayed turn from its delay phase instead of waiting it out.
func TestScheduleTurnsDeadSocket(t *testing.T) {
	r := &Runner{opt: Options{}}
	r.opt.Logf = func(string, ...any) {}

	steerSvr := daemonclient.New(deadSocket(t))
	defer steerSvr.Close()

	turns := []suite.Turn{
		{DelayS: 0, Message: "no delay turn"},
		{DelayS: 100, Message: "far future turn"},
	}
	sctx, cancel := context.WithCancel(context.Background())
	target := &steerTarget{conversationID: "conv-does-not-exist"}
	events, wait := r.scheduleTurns(sctx, steerSvr, target, turns, time.Now())

	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()
	wait()

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Message != turns[0].Message || events[0].DelayS != 0 {
		t.Errorf("events[0] = %+v", events[0])
	}
	if events[1].DelayS != 100 {
		t.Errorf("events[1].DelayS = %d, want 100", events[1].DelayS)
	}
	if events[0].Accepted {
		t.Errorf("events[0] accepted against a dead socket: %+v", events[0])
	}
	if events[0].Error == "" {
		t.Errorf("events[0] should record a delivery failure, got %+v", events[0])
	}
	if events[1].Error == "" {
		t.Errorf("events[1] should record cancellation, got %+v", events[1])
	}
}

// TestScheduleTurnsDelayCancellation checks the delay-phase cancel path in
// isolation: cancelling before the delay elapses must record a cancellation
// error and not attempt delivery.
func TestScheduleTurnsDelayCancellation(t *testing.T) {
	r := &Runner{opt: Options{}}
	r.opt.Logf = func(string, ...any) {}

	steerSvr := daemonclient.New(deadSocket(t))
	defer steerSvr.Close()

	sctx, cancel := context.WithCancel(context.Background())
	events, wait := r.scheduleTurns(sctx, steerSvr, &steerTarget{conversationID: "conv-x"},
		[]suite.Turn{{DelayS: 3600, Message: "never delivered"}}, time.Now())
	cancel() // cancel while the turn is still waiting out its delay
	wait()

	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Accepted || events[0].PathUsed != "" {
		t.Errorf("delivery should not have been attempted: %+v", events[0])
	}
	if events[0].Error == "" {
		t.Error("expected a cancellation error on the turn event")
	}
}

// TestSteerTargetResolution verifies the candidate-ID ordering: resolved
// thread ID first (when available), then the deterministic default-thread
// derivation, then the raw session conversation ID.
func TestSteerTargetResolution(t *testing.T) {
	// No resolution: derivation + raw ID.
	plain := &steerTarget{conversationID: "conv-088f38534bc14426"}
	got := plain.ids()
	want := []string{"conv-088f38534bc14426-thread-general-4426", "conv-088f38534bc14426"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ids() = %v, want %v", got, want)
	}

	// Resolved thread ID is prepended.
	ch := make(chan string, 1)
	ch <- "conv-session-thread-work-abcd"
	withResolution := &steerTarget{conversationID: "conv-session", resolved: ch}
	got = withResolution.ids()
	if len(got) != 3 || got[0] != "conv-session-thread-work-abcd" {
		t.Errorf("ids() with resolution = %v, want resolved first", got)
	}
	// Resolution is cached: draining the channel keeps the resolved ID.
	if got := withResolution.ids(); len(got) != 3 || got[0] != "conv-session-thread-work-abcd" {
		t.Errorf("ids() cached = %v, want resolved first", got)
	}
}

// TestDeriveThreadConv pins the derivation against meept's
// TopicDetector.GenerateThreadID + Session.GetOrCreateThread convention.
func TestDeriveThreadConv(t *testing.T) {
	cases := map[string]string{
		"conv-83ee881874f24ce2":      "conv-83ee881874f24ce2-thread-general-4ce2",
		"conv-abcd":                  "conv-abcd-thread-general-abcd", // short ID: full string as suffix
		"conv-x-thread-general-yyyy": "",                              // already a thread ID
		"session-1234":               "",                              // not a conversation ID
		"":                           "",
	}
	for in, want := range cases {
		if got := deriveThreadConv(in); got != want {
			t.Errorf("deriveThreadConv(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestIsQueueGone covers the error classification that decides whether the
// scheduler falls through to the next delivery path.
func TestIsQueueGone(t *testing.T) {
	cases := []struct {
		path string
		err  error
		want bool
	}{
		{"steer", errors.New("rpc error 1: queue not found"), true},
		{"steer", errors.New("steer failed: queue is closed"), true},
		{"followup", errors.New("rpc error 1: queue not found"), true},
		{"followup", errors.New("follow-up failed: queue not found"), true},
		{"steer", nil, false},
		{"steer", errors.New("dial /tmp/nope.sock: connection refused"), false},
		{"chat", errors.New("rpc error 1: queue not found"), false}, // chat never counts as queue-gone
		{"steer", errors.New("invalid params: message is required"), false},
	}
	for _, tc := range cases {
		if got := isQueueGone(tc.path, tc.err); got != tc.want {
			t.Errorf("isQueueGone(%q, %v) = %v, want %v", tc.path, tc.err, got, tc.want)
		}
	}
}

// TestTurnEventTranscriptJSON pins the transcript wire shape: turns serialise
// under "turns" with the fields downstream tooling reads.
func TestTurnEventTranscriptJSON(t *testing.T) {
	tr := results.Transcript{
		Suite: "steering", TaskID: "midrun", Attempt: 1,
		Turns: []results.TurnEvent{{
			Message:  "name it result2.txt",
			DelayS:   5,
			SentAt:   time.Unix(1700000000, 0).UTC(),
			PathUsed: "steer",
			Accepted: true,
		}},
	}
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	turns, ok := raw["turns"].([]any)
	if !ok || len(turns) != 1 {
		t.Fatalf("transcript JSON missing turns array: %s", b)
	}
	ev, ok := turns[0].(map[string]any)
	if !ok {
		t.Fatalf("turn[0] not an object: %s", b)
	}
	if ev["path_used"] != "steer" || ev["accepted"] != true || ev["delay_s"] != float64(5) {
		t.Errorf("turn[0] wire shape wrong: %v", ev)
	}
}
