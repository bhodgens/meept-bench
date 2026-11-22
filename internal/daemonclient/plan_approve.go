package daemonclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Plan-approval driving for the strategic-planner approval gate (plan
// 20261119-usability-remediation, u-plan residual). When a chat turn's task
// parks at task.StateAwaitingApproval ("Plan awaiting user approval" --
// the complexity gate orchestrator.task_approval_step_threshold, default 5),
// an interactive client (TUI/CLI/GUI) shows the approval card and the human
// approves via task.approve / plan.approve. A headless bench run has no
// human; the runner is the de-facto interactive client.
//
// The approval RPC itself is the daemon's EXISTING surface, unchanged:
//
//	task.approve  {task_id} -> {task_id, status:"approved"}
//
// registered by TaskApprovalHandler.RegisterTaskApprovalMethods
// (internal/rpc/task_approval.go, wired in internal/daemon/daemon.go) and
// routing to StrategicPlanner.ApprovePlan: persists the parked pending
// steps, sets UserApproved, transitions to StateExecuting, and schedules
// the steps -- after which the turn's real terminal event fires and the
// ordinary ChatAsync wait completes.
//
// plan.approve is deliberately NOT used here: it drives the SEPARATE plan
// store lifecycle (internal/plan manager, plan_id-keyed) and fails with
// "plan not found" for a task parked by the strategic planner's approval
// gate, whose plans are task-metadata-backed (no plan_id is ever minted).

// ApproveTask approves a task parked at the plan approval gate via the
// daemon's existing task.approve RPC. err is nil when the daemon reports
// status "approved".
func (c *Client) ApproveTask(ctx context.Context, taskID string) error {
	var out json.RawMessage
	err := c.Call(ctx, "task.approve", map[string]any{"task_id": taskID}, &out)
	if err != nil {
		return fmt.Errorf("task.approve: %w", err)
	}
	var parsed struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if jsonErr := json.Unmarshal(out, &parsed); jsonErr != nil {
		return fmt.Errorf("task.approve: parse response: %w", jsonErr)
	}
	if parsed.Status != "approved" {
		return fmt.Errorf("task.approve: status %q, want approved", parsed.Status)
	}
	return nil
}

// findTaskBySessionSince locates the session's task created at/after since
// (already scoped by findTaskBySession) and returns its current state.
func (c *Client) findTaskBySessionSince(sessionID string, since time.Time) (taskID, state string, found bool, err error) {
	taskID, state, _, found, err = c.findTaskBySession(sessionID, since)
	return taskID, state, found, err
}

// approvalApprovePollInterval is the poll cadence for the approve driver.
const approvalApprovePollInterval = time.Second

// ApprovePollInterval overrides the approve driver's poll cadence (tests
// only; zero keeps the 1s default).
var ApprovePollInterval = approvalApprovePollInterval

// FindAndApproveParkedTask polls task.list_extended for the session's
// task created since `since` until it reaches StateAwaitingApproval, then
// approves it via the daemon's existing task.approve RPC. Non-approval
// states return immediately (nothing to approve: pending/executing/
// completed/... mean the park never happened or already resolved).
//
// It exists because the parked turn's own turn.terminal stream carries the
// park notice but not the task ID (the strategic planner publishes it on
// the task.pending_approval topic, which the bench does not subscribe to),
// so the approver re-derives the task through the session linkage the
// plan-compiler seal driver already uses (plan_seal.go findTaskBySession).
func (c *Client) FindAndApproveParkedTask(ctx context.Context, sessionID string, since time.Time) (string, error) {
	interval := ApprovePollInterval
	if interval <= 0 {
		interval = approvalApprovePollInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for i := 0; i < 300; i++ { // bounded: 300 * interval (default 300 x 1s = 5 min)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-tick.C:
		}
		taskID, state, found, err := c.findTaskBySessionSince(sessionID, since)
		if err != nil || !found {
			continue // task not created yet / transient list error
		}
		switch state {
		case "awaiting_approval":
			if err := c.ApproveTask(ctx, taskID); err != nil {
				return taskID, err
			}
			return taskID, nil
		case "pending", "planning":
			// Park not reached yet (or a draft seal path); keep polling
			// briefly. The park transition typically lands within a few
			// seconds of the park notice.
		default:
			// executing/completed/failed/cancelled/rejected: the gate
			// already resolved; nothing to approve.
			return taskID, nil
		}
	}
	return "", fmt.Errorf("plan auto-approve: task for session %s never reached awaiting_approval within the poll window", sessionID)
}
