package daemonclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// asyncMethod records one RPC method invocation for call-order assertions.
type asyncMethod struct {
	Method string
	Params map[string]any
}

// fakeDaemonScripted extends the plain fake daemon with chat.submit, ack
// latency, and turn.terminal delivery via bus.subscribe/bus.poll. It is a
// test-only harness; all state is guarded by mu.
type fakeDaemonScripted struct {
	mu sync.Mutex

	ln net.Conn // listener (kept raw to avoid confusion with conn)

	sockPath string

	methods []asyncMethod // every RPC method served, in order

	ackLatency time.Duration    // delay before chat.submit answers
	ack        ChatAck          // chat.submit response
	terminal   []map[string]any // queued turn.terminal payloads (delivered after pollPause)
	pollPause  time.Duration    // delay before bus.poll answers
	pollErr    error            // transient error to return once (optional)
	subscribed bool             // a bus.subscribe happened before chat.submit
	buf        *bufio.Reader    // per-connection reader (single-conn tests)
	conn       net.Conn         // the single served connection
	respCh     chan struct{}    // signals "response written"
	pollCount  int              // number of bus.poll calls served
	closeOnce  sync.Once        // guard for cleanup
	cleanupFn  func()           // full teardown
}

// startAsyncDaemon spins up the scripted fake and returns its socket path.
// Script fields (ack, terminal, ackLatency, pollPause, pollErr) may be set
// on the returned struct before the client dials; the test harness serves
// lazily per frame so pre-set scripting applies.
func startAsyncDaemon(t *testing.T) *fakeDaemonScripted {
	t.Helper()
	dir, err := os.MkdirTemp("", "meept-bench-async")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "async.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDaemonScripted{sockPath: sock, respCh: make(chan struct{}, 64)}
	t.Cleanup(func() {
		f.closeOnce.Do(func() {
			ln.Close()
			if f.conn != nil {
				f.conn.Close()
			}
			os.RemoveAll(dir)
		})
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conn = conn
			f.buf = bufio.NewReader(conn)
			f.mu.Unlock()
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeDaemonScripted) serve(conn net.Conn) {
	for {
		f.mu.Lock()
		buf := f.buf
		f.mu.Unlock()
		if buf == nil {
			return
		}
		payload, err := readFrame(buf)
		if err != nil {
			return
		}
		var req rpcRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return
		}
		resp := f.handle(req)
		out, err := json.Marshal(resp)
		if err != nil {
			return
		}
		if err := writeFrame(conn, out); err != nil {
			return
		}
		f.respCh <- struct{}{}
	}
}

func (f *fakeDaemonScripted) handle(req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.methods = append(f.methods, asyncMethod{Method: req.Method})
	if req.Params != nil {
		_ = json.Unmarshal(req.Params, &f.methods[len(f.methods)-1].Params)
	}
	switch req.Method {
	case "ping":
		resp.Result = []byte(`"pong"`)
	case "bus.subscribe":
		f.subscribed = true
		resp.Result = []byte(`{"subscription_id":"sub-1"}`)
	case "bus.poll":
		f.pollCount++
		if f.pollPause > 0 {
			time.Sleep(f.pollPause)
		}
		if f.pollErr != nil {
			resp.Error = &rpcError{Code: -32000, Message: f.pollErr.Error()}
			f.pollErr = nil // transient: once only
			return resp
		}
		evts := make([]map[string]any, 0, len(f.terminal))
		for _, p := range f.terminal {
			evts = append(evts, map[string]any{
				"topic":     "turn.terminal",
				"type":      "turn.terminal",
				"source":    "agent",
				"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
				"payload":   p,
			})
		}
		f.terminal = nil // deliver once
		b, _ := json.Marshal(map[string]any{"events": evts})
		resp.Result = b
	case "chat.submit":
		if f.ackLatency > 0 {
			time.Sleep(f.ackLatency)
		}
		ack := f.ack
		b, _ := json.Marshal(ack)
		resp.Result = b
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

// recordedMethods snapshots the served method sequence.
func (f *fakeDaemonScripted) recordedMethods(t *testing.T) []asyncMethod {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]asyncMethod, len(f.methods))
	copy(out, f.methods)
	return out
}

// subscribesBeforeSubmit asserts bus.subscribe landed before chat.submit.
func subscribesBeforeSubmit(t *testing.T, methods []asyncMethod) {
	t.Helper()
	seenSub, seenSubmit := -1, -1
	for i, m := range methods {
		switch m.Method {
		case "bus.subscribe":
			if seenSub == -1 {
				seenSub = i
			}
		case "chat.submit":
			if seenSubmit == -1 {
				seenSubmit = i
			}
		}
	}
	if seenSubmit == -1 {
		t.Fatalf("no chat.submit served; methods: %v", methodNames(methods))
	}
	if seenSub == -1 || seenSub > seenSubmit {
		t.Fatalf("bus.subscribe must precede chat.submit (no event gap); methods: %v", methodNames(methods))
	}
}

func methodNames(methods []asyncMethod) []string {
	out := make([]string, len(methods))
	for i, m := range methods {
		out[i] = m.Method
	}
	return out
}

func TestChatAsyncHappyPath(t *testing.T) {
	f := startAsyncDaemon(t)
	f.ack = ChatAck{TurnID: "t-1", ConversationID: "conv-1", SessionID: "session-1", Accepted: true}
	f.terminal = []map[string]any{{
		"conversation_id": "conv-1",
		"turn_id":         "t-1",
		"status":          "completed",
		"reply":           "done",
		"duration_ms":     42,
	}}

	c := New(f.sockPath)
	metrics := &ChatMetrics{}
	res, err := c.ChatAsync(context.Background(), "hello", "session-1", metrics)
	if err != nil {
		t.Fatalf("ChatAsync: %v", err)
	}
	if res.Reply != "done" {
		t.Errorf("Reply = %q, want %q", res.Reply, "done")
	}
	if res.Status != "completed" {
		t.Errorf("Status = %q, want %q", res.Status, "completed")
	}
	if res.Error != "" {
		t.Errorf("Error = %q, want empty", res.Error)
	}
	if res.DurationMS != 42 {
		t.Errorf("DurationMS = %d, want 42", res.DurationMS)
	}
	if metrics.AckSeconds <= 0 {
		t.Errorf("AckSeconds = %v, want > 0", metrics.AckSeconds)
	}
	if metrics.TurnSeconds <= 0 {
		t.Errorf("TurnSeconds = %v, want > 0", metrics.TurnSeconds)
	}
	subscribesBeforeSubmit(t, f.recordedMethods(t))
}

func TestChatAsyncSubmitParams(t *testing.T) {
	f := startAsyncDaemon(t)
	f.ack = ChatAck{TurnID: "t-9", Accepted: true}
	f.terminal = []map[string]any{{"turn_id": "t-9", "status": "completed", "reply": "ok"}}

	c := New(f.sockPath)
	if _, err := c.ChatAsync(context.Background(), "hi there", "session-abc", nil); err != nil {
		t.Fatalf("ChatAsync: %v", err)
	}
	var submit *asyncMethod
	for i, m := range f.recordedMethods(t) {
		if m.Method == "chat.submit" {
			submit = &f.methods[i]
			break
		}
	}
	if submit == nil {
		t.Fatal("chat.submit not served")
	}
	if submit.Params["message"] != "hi there" {
		t.Errorf("message = %v, want hi there", submit.Params["message"])
	}
	if submit.Params["session_id"] != "session-abc" {
		t.Errorf("session_id = %v, want session-abc", submit.Params["session_id"])
	}
	if submit.Params["source_client"] != "meept-bench" {
		t.Errorf("source_client = %v, want meept-bench", submit.Params["source_client"])
	}
}

func TestChatAsyncTurnIDFilter(t *testing.T) {
	f := startAsyncDaemon(t)
	f.ack = ChatAck{TurnID: "t-1", Accepted: true}
	// An unrelated terminal event arrives first; the matching one follows.
	f.terminal = []map[string]any{
		{"turn_id": "t-other", "status": "completed", "reply": "not mine"},
		{"turn_id": "t-1", "status": "completed", "reply": "mine"},
	}

	c := New(f.sockPath)
	res, err := c.ChatAsync(context.Background(), "hello", "session-1", nil)
	if err != nil {
		t.Fatalf("ChatAsync: %v", err)
	}
	if res.Reply != "mine" {
		t.Errorf("Reply = %q, want %q (unrelated event must be ignored)", res.Reply, "mine")
	}
}

func TestChatAsyncFailedTurnIsValidOutcome(t *testing.T) {
	f := startAsyncDaemon(t)
	f.ack = ChatAck{TurnID: "t-2", Accepted: true}
	f.terminal = []map[string]any{
		{"turn_id": "t-2", "status": "failed", "reply": "", "error": "agent exploded"},
	}

	c := New(f.sockPath)
	res, err := c.ChatAsync(context.Background(), "hello", "session-1", nil)
	if err != nil {
		t.Fatalf("failed turn must not be an error, got: %v", err)
	}
	if res.Status != "failed" {
		t.Errorf("Status = %q, want %q", res.Status, "failed")
	}
	if res.Error == "" {
		t.Error("Error empty, want the daemon-side failure text")
	}
}

func TestChatAsyncCtxDeadline(t *testing.T) {
	f := startAsyncDaemon(t)
	f.ack = ChatAck{TurnID: "t-3", Accepted: true}
	// No terminal event ever arrives; the caller ctx expires first.

	c := New(f.sockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := c.ChatAsync(ctx, "hello", "session-1", nil)
	if err == nil {
		t.Fatal("want ctx deadline error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want errors.Is context.DeadlineExceeded", err)
	}
}

func TestChatAsyncLivenessStall(t *testing.T) {
	f := startAsyncDaemon(t)
	f.ack = ChatAck{TurnID: "t-4", Accepted: true}
	// No terminal event; liveness (50ms) fires well before the ctx.

	c := New(f.sockPath)
	c.LivenessTimeout = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := c.ChatAsync(ctx, "hello", "session-1", nil)
	if err == nil {
		t.Fatal("want ErrTurnStalled, got nil")
	}
	if !errors.Is(err, ErrTurnStalled) {
		t.Errorf("err = %v, want errors.Is ErrTurnStalled", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("stall returned after %s; liveness timeout not honored", elapsed)
	}
}

func TestChatAsyncRejectedAck(t *testing.T) {
	f := startAsyncDaemon(t)
	f.ack = ChatAck{TurnID: "", Accepted: false, Note: "no active session"}

	c := New(f.sockPath)
	_, err := c.ChatAsync(context.Background(), "hello", "session-1", nil)
	if err == nil {
		t.Fatal("want rejection error, got nil")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Errorf("err = %v, want it to contain the ack note", err)
	}
}

func TestChatAsyncSubscribeBeforeSubmit(t *testing.T) {
	f := startAsyncDaemon(t)
	f.ack = ChatAck{TurnID: "t-5", Accepted: true}
	f.terminal = []map[string]any{{"turn_id": "t-5", "status": "completed", "reply": "ok"}}

	c := New(f.sockPath)
	if _, err := c.ChatAsync(context.Background(), "hello", "session-1", nil); err != nil {
		t.Fatalf("ChatAsync: %v", err)
	}
	subscribesBeforeSubmit(t, f.recordedMethods(t))
}

func TestChatAsyncTransientPollErrorSurvives(t *testing.T) {
	f := startAsyncDaemon(t)
	f.ack = ChatAck{TurnID: "t-6", Accepted: true}
	f.terminal = []map[string]any{{"turn_id": "t-6", "status": "completed", "reply": "ok"}}
	f.pollErr = errors.New("bus hiccup")
	f.pollPause = 30 * time.Millisecond // keep the first failing poll inside the liveness window

	c := New(f.sockPath)
	c.LivenessTimeout = 2 * time.Second
	res, err := c.ChatAsync(context.Background(), "hello", "session-1", nil)
	if err != nil {
		t.Fatalf("transient poll error must not fail the turn: %v", err)
	}
	if res.Reply != "ok" {
		t.Errorf("Reply = %q, want %q", res.Reply, "ok")
	}
}

func TestChatAsyncFastTurnNeverMissesTerminal(t *testing.T) {
	// Stress variant: the daemon answers bus.poll only AFTER chat.submit
	// has been answered AND the terminal payload is queued from the very
	// first poll — proving subscribe-before-submit means no event gap.
	// f.terminal is re-armed per iteration (the fake delivers once per
	// arm) and ack/turn ids vary so a stale event can never satisfy a
	// later turn's turn_id filter.
	f := startAsyncDaemon(t)

	c := New(f.sockPath)
	for i := 0; i < 5; i++ {
		turnID := fmt.Sprintf("t-7-%d", i)
		f.mu.Lock()
		f.ack = ChatAck{TurnID: turnID, Accepted: true}
		f.terminal = []map[string]any{{"turn_id": turnID, "status": "completed", "reply": "instant"}}
		f.mu.Unlock()

		res, err := c.ChatAsync(context.Background(), fmt.Sprintf("hello %d", i), "session-1", nil)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if res.Reply != "instant" {
			t.Fatalf("iteration %d: Reply = %q, want instant", i, res.Reply)
		}
	}
}

func TestLivenessDefault(t *testing.T) {
	if DefaultLivenessTimeout != 300*time.Second {
		t.Errorf("DefaultLivenessTimeout = %v, want 300s", DefaultLivenessTimeout)
	}
	c := New("/nonexistent.sock")
	if got := c.liveness(); got != 300*time.Second {
		t.Errorf("default liveness() = %v, want 300s", got)
	}
	c.LivenessTimeout = 5 * time.Second
	if got := c.liveness(); got != 5*time.Second {
		t.Errorf("settable liveness() = %v, want 5s", got)
	}
}
