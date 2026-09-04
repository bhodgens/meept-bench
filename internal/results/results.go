// Package results defines the JSONL result row and transcript records.
package results

import (
	"encoding/json"
	"time"
)

// Row is one attempt's result, appended to results/<suite>/results.jsonl.
type Row struct {
	Suite        string    `json:"suite"`
	TaskID       string    `json:"task_id"`
	Attempt      int       `json:"attempt"`
	Seed         int64     `json:"seed"`
	Model        string    `json:"model,omitempty"`
	HFRevision   string    `json:"hf_revision,omitempty"`
	Verdict      string    `json:"verdict"` // pass | fail | error | timeout
	Passed       bool      `json:"passed"`
	TokensIn     int64     `json:"tokens_in,omitempty"`
	TokensOut    int64     `json:"tokens_out,omitempty"`
	CostUSD      float64   `json:"cost_usd,omitempty"`
	WallSeconds  float64   `json:"wall_seconds"`
	Checks       []any     `json:"checks,omitempty"`
	ErrorKind    string    `json:"error_kind,omitempty"`
	ErrorDetail  string    `json:"error_detail,omitempty"`
	AutoApproved bool      `json:"auto_approved,omitempty"` // disclosed per GAPS.md gap 6
	StartedAt    time.Time `json:"started_at"`
	WorktreeKept bool      `json:"worktree_kept,omitempty"`
	WorktreePath string    `json:"worktree_path,omitempty"`
}

// Transcript is the captured agent interaction for one attempt.
type Transcript struct {
	Suite                string      `json:"suite"`
	TaskID               string      `json:"task_id"`
	Attempt              int         `json:"attempt"`
	Seed                 int64       `json:"seed"`
	Prompt               string      `json:"prompt"`
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
