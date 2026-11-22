package daemonclient

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// chatApprovalHarness extends the scripted fake daemon with the
// task.approve / task.list_extended RPCs (plan-approval driving tests),
// intercepted before the base handler.
type chatApprovalHarness struct {
	*fakeDaemonScripted

	mu            sync.Mutex
	approveCalls  int
	approveTaskID string
	approveStatus string         // canned reply status ("" = approved)
	taskState     string         // state reported by task.list_extended
	armedTerminal map[string]any // terminal re-armed after the approve
	approved      chan struct{}  // closed on first task.approve
	armOnce       sync.Once
}

func startApprovalHarness(t *testing.T) *chatApprovalHarness {
	t.Helper()
	h := &chatApprovalHarness{
		approveStatus: "",
		taskState:     "awaiting_approval",
		approved:      make(chan struct{}),
	}
	h.fakeDaemonScripted = startAsyncDaemonWithHandler(t, func(f *fakeDaemonScripted, req rpcRequest) rpcResponse {
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		var params map[string]any
		_ = json.Unmarshal(req.Params, &params)
		switch req.Method {
		case "task.approve":
			h.mu.Lock()
			h.approveCalls++
			h.approveTaskID, _ = params["task_id"].(string)
			status := h.approveStatus
			if status == "" {
				status = "approved"
			}
			h.mu.Unlock()
			b, _ := json.Marshal(map[string]any{"task_id": h.approveTaskID, "status": status})
			resp.Result = b
		case "task.list_extended":
			h.mu.Lock()
			state := h.taskState
			h.mu.Unlock()
			b, _ := json.Marshal(map[string]any{"tasks": []map[string]any{{
				"id": "task-plan-1", "state": state, "description": "plan it",
				"created_at": time.Now().UTC().Format(time.RFC3339), "linked_sessions": []string{"session-plan"},
			}}})
			resp.Result = b
		}
		return resp
	})
	// Park notice on the ChatAsync stream.
	h.mu.Lock()
	h.ack = ChatAck{TurnID: "t-park", Accepted: true}
	h.terminal = []map[string]any{
		{"turn_id": "t-park", "status": "parked", "error": "Plan awaiting user approval", "reply": ""},
	}
	h.mu.Unlock()
	// Watch bus.poll signals (highest-frequency RPC served) and re-arm the
	// real terminal once the approve has landed, so the ordinary wait
	// continues to a completed terminal.
	go func() {
		for range h.respCh {
			h.mu.Lock()
			approved := h.approveCalls > 0
			arm := h.armedTerminal
			h.mu.Unlock()
			if approved && arm != nil {
				h.armOnce.Do(func() {
					h.mu.Lock()
					h.terminal = []map[string]any{arm}
					h.mu.Unlock()
					close(h.approved)
				})
				return
			}
		}
	}()
	return h
}

func approvalCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// Harness A: ApproveTask over the real wire (reply shape + params).
func TestApproveTaskWire(t *testing.T) {
	h := startApprovalHarness(t)
	c := New(h.sockPath)
	ctx, cancel := contextWithTimeout(3 * time.Second)
	defer cancel()
	if err := c.ApproveTask(ctx, "task-77"); err != nil {
		t.Fatalf("ApproveTask: %v", err)
	}
	h.mu.Lock()
	calls, id := h.approveCalls, h.approveTaskID
	h.mu.Unlock()
	if calls != 1 || id != "task-77" {
		t.Fatalf("task.approve served %d times, task_id=%q; want 1 call with task-77", calls, id)
	}

	// Non-approved status must error (daemon refused: e.g. wrong state).
	h.mu.Lock()
	h.approveStatus = "rejected"
	h.mu.Unlock()
	if err := c.ApproveTask(ctx, "task-77"); err == nil {
		t.Fatal("ApproveTask must error on a non-approved status reply")
	}
}

// Harness B: the parked turn fires exactly one task.approve and then
// completes on the re-armed real terminal.
func TestChatAsyncAutoApprovesPlanPark(t *testing.T) {
	h := startApprovalHarness(t)
	// Re-arm the completed terminal once the approve lands.
	h.mu.Lock()
	h.armedTerminal = map[string]any{
		"turn_id": "t-park", "status": "completed", "reply": "plan executed", "duration_ms": 5,
	}
	h.mu.Unlock()
	var cbOK bool
	var cbTaskID, cbDetail string
	opts := []*ChatApprovalOptions{{
		ApproveWhenParked: func(ctx context.Context) (string, error) {
			// Drive the real FindAndApproveParkedTask path over a forked
			// connection, exactly like the runner does.
			ac := New(h.sockPath)
			defer ac.Close()
			prev := ApprovePollInterval
			ApprovePollInterval = 10 * time.Millisecond
			t.Cleanup(func() { ApprovePollInterval = prev })
			return ac.FindAndApproveParkedTask(ctx, "session-plan", time.Now().Add(-time.Minute))
		},
		OnAutoApprove: func(ok bool, taskID, detail string) {
			cbOK, cbTaskID, cbDetail = ok, taskID, detail
		},
	}}
	c := New(h.sockPath)
	c.LivenessTimeout = 3 * time.Second
	res, err := c.ChatAsync(approvalCtx(t), "plan something", "session-plan", nil, opts...)
	if err != nil {
		t.Fatalf("ChatAsync with approver: %v", err)
	}
	if res.Status != "completed" || res.Reply != "plan executed" {
		t.Fatalf("post-approval terminal: %+v", res)
	}
	h.mu.Lock()
	calls, id := h.approveCalls, h.approveTaskID
	h.mu.Unlock()
	if calls != 1 {
		t.Errorf("task.approve served %d times, want exactly 1 (one attempt per turn)", calls)
	}
	if id != "task-plan-1" {
		t.Errorf("approved task_id = %q, want task-plan-1 (session linkage derived)", id)
	}
	if !cbOK || cbTaskID != "task-plan-1" || cbDetail == "" {
		t.Errorf("OnAutoApprove = %v/%q/%q, want true/task-plan-1/non-empty", cbOK, cbTaskID, cbDetail)
	}
}

// Without the opt-in no approve is ever served and the park is returned.
func TestChatAsyncAutoApproveDisabledByDefault(t *testing.T) {
	h := startApprovalHarness(t)
	c := New(h.sockPath)
	c.LivenessTimeout = 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.ChatAsync(ctx, "plan something", "session-plan", nil)
	if err == nil || res != nil {
		t.Fatalf("park without opt-in must stall, got res=%v err=%v", res, err)
	}
	h.mu.Lock()
	calls := h.approveCalls
	h.mu.Unlock()
	if calls != 0 {
		t.Errorf("task.approve served %d times without opt-in; the approval gate must stay default-on", calls)
	}
}

// A failing approve keeps the park (exactly as without the flag).
func TestChatAsyncAutoApproveFailureKeepsPark(t *testing.T) {
	h := startApprovalHarness(t)
	called := false
	opts := []*ChatApprovalOptions{{
		ApproveWhenParked: func(ctx context.Context) (string, error) {
			called = true
			return "", context.DeadlineExceeded
		},
	}}
	c := New(h.sockPath)
	c.LivenessTimeout = 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if res, err := c.ChatAsync(ctx, "plan something", "session-plan", nil, opts...); err == nil || res != nil {
		t.Fatalf("failed approve must keep the park, got res=%v err=%v", res, err)
	}
	if !called {
		t.Fatal("ApproveWhenParked never invoked")
	}
}

// FindAndApproveParkedTask: non-approval states return immediately with no
// approve fired.
func TestFindAndApproveSkipsResolvedStates(t *testing.T) {
	h := startApprovalHarness(t)
	h.mu.Lock()
	h.taskState = "executing"
	h.mu.Unlock()
	c := New(h.sockPath)
	ctx, cancel := contextWithTimeout(3 * time.Second)
	defer cancel()
	taskID, err := c.FindAndApproveParkedTask(ctx, "session-plan", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("FindAndApproveParkedTask: %v", err)
	}
	if taskID != "task-plan-1" {
		t.Errorf("taskID = %q, want task-plan-1", taskID)
	}
	h.mu.Lock()
	calls := h.approveCalls
	h.mu.Unlock()
	if calls != 0 {
		t.Errorf("task.approve fired %d times for an executing task; resolved states must return without approving", calls)
	}
}

// FindAndApproveParkedTask: timeout when the task never parks.
func TestFindAndApproveTimeoutWhenNeverParked(t *testing.T) {
	h := startApprovalHarness(t)
	h.mu.Lock()
	h.taskState = "pending"
	h.mu.Unlock()
	c := New(h.sockPath)
	prev := ApprovePollInterval
	ApprovePollInterval = 10 * time.Millisecond
	t.Cleanup(func() { ApprovePollInterval = prev })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.FindAndApproveParkedTask(ctx, "session-plan", time.Now().Add(-time.Minute)); err == nil {
		t.Fatal("want timeout error when the task never reaches awaiting_approval")
	}
}
