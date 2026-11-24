package daemonclient

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Plan-compiler seal driving for the plan-compiler pipeline (async-turn
// migration follow-up, gate 2026-09-18). With plans.plan_compiler_enabled=true
// the daemon parks plan-mode tasks in the planning state behind a brainstorm
// draft. The seeded draft is the raw document scaffold (placeholder title,
// goal, open questions) — deliberately human-in-the-loop. An autonomous
// client must therefore do three things the interactive user would do:
//
//  1. find the session's parked task (task.list_extended)
//  2. refine the draft scaffold into a minimal concrete plan (plan.draft
//     get → fill → save). A chat follow-up does NOT do this: the daemon
//     routes it as a NEW task (verified 2026-09-18, fresh-rig daemon).
//  3. seal + compile + execute (plan.seal)
//
// Sealing is idempotent (persistedHashes marker).

// sealPollInterval is the polling cadence for the seal driver.
const sealPollInterval = 2 * time.Second

// SealPlan seals and schedules the brainstorm draft for a task.
func (c *Client) SealPlan(ctx context.Context, taskID string) error {
	var out json.RawMessage
	err := c.Call(ctx, "plan.seal", map[string]any{"task_id": taskID}, &out)
	if err != nil {
		return fmt.Errorf("plan.seal: %w", err)
	}
	var parsed struct {
		Status   string   `json:"status"`
		Problems []string `json:"problems"`
	}
	if jsonErr := json.Unmarshal(out, &parsed); jsonErr != nil {
		return fmt.Errorf("plan.seal: parse response: %w", jsonErr)
	}
	if parsed.Status == "problems" {
		return fmt.Errorf("plan.seal: %d compile problems", len(parsed.Problems))
	}
	return nil
}

// GetDraft returns the task's current brainstorm draft markdown.
func (c *Client) GetDraft(ctx context.Context, taskID string) (string, error) {
	var out json.RawMessage
	err := c.Call(ctx, "plan.draft", map[string]any{"task_id": taskID}, &out)
	if err != nil {
		return "", fmt.Errorf("plan.draft get: %w", err)
	}
	var parsed struct {
		Markdown string `json:"markdown"`
	}
	if jsonErr := json.Unmarshal(out, &parsed); jsonErr != nil {
		return "", fmt.Errorf("plan.draft get: parse: %w", jsonErr)
	}
	return parsed.Markdown, nil
}

// SaveDraft replaces the task's brainstorm draft.
func (c *Client) SaveDraft(ctx context.Context, taskID, markdown string) error {
	var out json.RawMessage
	err := c.Call(ctx, "plan.draft", map[string]any{"task_id": taskID, "markdown": markdown}, &out)
	if err != nil {
		return fmt.Errorf("plan.draft save: %w", err)
	}
	return nil
}

// templatePlaceholder matches unfilled scaffold slots.
var templatePlaceholder = regexp.MustCompile(`<[^>\n]{3,80}>`)

// refineDraft turns the seeded scaffold into a minimal single-phase plan
// concrete enough for the deterministic compiler to accept. The task's own
// description IS the spec for these regression-scale tasks; a single step
// delegating it to the assigned agent preserves semantics without the bench
// second-guessing the daemon's planner.
func refineDraft(md, taskID, description string) string {
	// Already concrete (no scaffold placeholders): keep it.
	if !templatePlaceholder.MatchString(md) {
		return md
	}

	updated := time.Now().UTC().Format("2006-01-02")
	return fmt.Sprintf(`# Plan: %s

## Meta

- task_id: %s
- version: 1
- status: draft
- updated: %s

## Goal

%s

## Decisions

- Decision: execute as a single direct step — Rationale: regression-scale task with one concrete deliverable

## Open Questions

## Phases

### Phase 1: execute the task

%s

**Produces:**

- `+"`task-output`"+` (file) — the task's requested deliverable

**Consumes:** none

**Steps:**

1. Perform the task: %s [code]

## Notes

- Draft refined autonomously by the bench client (plan-compiler seal drive).
`, firstLine(description), taskID, updated, description, description, description)
}

// firstLine trims a description to a sane single-line goal.
func firstLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r", ""))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		s = "complete the task"
	}
	return s
}

// findTaskBySession returns the first task in task.list_extended whose
// linked_sessions contains sessionID and whose creation time is at or after
// since. found=false when no such task exists yet.
func (c *Client) findTaskBySession(sessionID string, since time.Time) (taskID, state, description string, found bool, err error) {
	var out struct {
		Tasks []struct {
			ID             string   `json:"id"`
			State          string   `json:"state"`
			Description    string   `json:"description"`
			CreatedAt      string   `json:"created_at"`
			LinkedSessions []string `json:"linked_sessions"`
		} `json:"tasks"`
	}
	if err := c.Call(context.Background(), "task.list_extended", nil, &out); err != nil {
		return "", "", "", false, err
	}
	for _, t := range out.Tasks {
		hasSession := false
		for _, s := range t.LinkedSessions {
			if s == sessionID {
				hasSession = true
				break
			}
		}
		if !hasSession {
			continue
		}
		created, perr := time.Parse(time.RFC3339, t.CreatedAt)
		if perr == nil && created.Before(since.Add(-time.Second)) {
			continue // predates this submit; a different turn's task
		}
		return t.ID, t.State, t.Description, true, nil
	}
	return "", "", "", false, nil
}

// RefineAndSeal finds the task linked to sessionID created after since, and
// when it parks in the planning state behind a scaffold draft: refines the
// draft and seals it. Non-planning states return immediately (the task needs
// no seal). Returns the matched task id.
func (c *Client) RefineAndSeal(ctx context.Context, sessionID string, since time.Time) (string, error) {
	tick := time.NewTicker(sealPollInterval)
	defer tick.Stop()

	for i := 0; i < 300; i++ { // bounded: 300 * 2s = 10 min
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-tick.C:
		}

		taskID, state, description, found, err := c.findTaskBySession(sessionID, since)
		if err != nil || !found {
			continue // transient list error, or task not created yet
		}

		switch state {
		case "planning":
			// Parked behind the draft. Refine + seal.
		case "pending", "executing", "completed", "failed", "cancelled":
			return taskID, nil // nothing to seal
		default:
			return taskID, nil // unknown state; don't interfere
		}

		md, err := c.GetDraft(ctx, taskID)
		if err != nil {
			return taskID, err
		}
		refined := refineDraft(md, taskID, description)
		if refined != md {
			if err := c.SaveDraft(ctx, taskID, refined); err != nil {
				return taskID, err
			}
		}
		return taskID, c.SealPlan(ctx, taskID)
	}
	return "", fmt.Errorf("plan seal: task for session %s never appeared within the poll window", sessionID)
}

// SealTaskBySession is the legacy entry point: seal without refinement.
func (c *Client) SealTaskBySession(ctx context.Context, sessionID string, since time.Time) (string, error) {
	return c.RefineAndSeal(ctx, sessionID, since)
}
