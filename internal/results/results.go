// Package results defines the JSONL result row and transcript records.
package results

import (
	"encoding/json"
	"time"
)

// Row is one attempt's result, appended to results/<suite>/results.jsonl.
type Row struct {
	Suite        string  `json:"suite"`
	TaskID       string  `json:"task_id"`
	Attempt      int     `json:"attempt"`
	Seed         int64   `json:"seed"`
	Model        string  `json:"model,omitempty"`
	HFRevision   string  `json:"hf_revision,omitempty"`
	Verdict      string  `json:"verdict"` // pass | fail | error | timeout
	Passed       bool    `json:"passed"`
	TokensIn     int64   `json:"tokens_in,omitempty"`
	TokensOut    int64   `json:"tokens_out,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	WallSeconds  float64 `json:"wall_seconds"`
	Checks       []any   `json:"checks,omitempty"`
	ErrorKind    string  `json:"error_kind,omitempty"`
	ErrorDetail  string  `json:"error_detail,omitempty"`
	AutoApproved bool    `json:"auto_approved,omitempty"` // disclosed per GAPS.md gap 6
	// DeterministicFetch marks rows that ran under the daemon's
	// deterministic (cached-fetch) tool mode (task declared
	// tools.cached_fetch and the daemon preflight confirmed the gate).
	DeterministicFetch bool      `json:"deterministic_fetch,omitempty"`
	StartedAt          time.Time `json:"started_at"`
	WorktreeKept       bool      `json:"worktree_kept,omitempty"`
	WorktreePath       string    `json:"worktree_path,omitempty"`
}

// TurnEvent records one follow-up/steering turn scheduled by the task and
// how its delivery went. Appearance in the transcript proves the steering
// mechanics fired; PathUsed and Accepted capture which daemon queue took
// the message.
type TurnEvent struct {
	// Message is the follow-up text sent.
	Message string `json:"message"`
	// DelayS is the configured wait (seconds after the primary prompt).
	DelayS int `json:"delay_s"`
	// SentAt is when the runner actually delivered the message.
	SentAt time.Time `json:"sent_at"`
	// PathUsed is which daemon RPC accepted the message:
	// "steer" | "followup" | "chat".
	PathUsed string `json:"path_used"`
	// Accepted is true when the daemon reported the message queued.
	Accepted bool `json:"accepted"`
	// Error carries the delivery failure, if any.
	Error string `json:"error,omitempty"`
}

// Transcript is the captured agent interaction for one attempt.
type Transcript struct {
	Suite                string      `json:"suite"`
	TaskID               string      `json:"task_id"`
	Attempt              int         `json:"attempt"`
	Seed                 int64       `json:"seed"`
	Prompt               string      `json:"prompt"`
	Turns                []TurnEvent `json:"turns,omitempty"`
	FinalReply           string      `json:"final_reply"`
	RoutedAgent          string      `json:"routed_agent,omitempty"`
	ClassificationMethod string      `json:"classification_method,omitempty"`
	Error                string      `json:"error,omitempty"`
	ToolTrace            []ToolEvent `json:"tool_trace,omitempty"`
	StartedAt            time.Time   `json:"started_at"`
	EndedAt              time.Time   `json:"ended_at"`
}

// ToolEvent is one tool.execution.progress event distilled from the bus.
type ToolEvent struct {
	At     time.Time       `json:"at"`
	Topic  string          `json:"topic"`
	Type   string          `json:"type"`
	Source string          `json:"source"`
	Raw    json.RawMessage `json:"payload"`
}
