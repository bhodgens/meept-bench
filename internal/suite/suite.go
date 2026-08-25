// Package suite defines the benchmark task-manifest schema and its loader.
package suite

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Manifest is a suite file (JSON) listing tasks to run.
type Manifest struct {
	Suite       string            `json:"suite"`
	Description string            `json:"description,omitempty"`
	Internal    bool              `json:"internal,omitempty"` // scores not publishable per BENCHMARKS.md rules
	HFRevision  string            `json:"hf_revision,omitempty"`
	Model       string            `json:"model,omitempty"`
	Tasks       []Task            `json:"tasks"`
	Meta        map[string]string `json:"meta,omitempty"`
}

// Task is one benchmark item.
type Task struct {
	ID        string   `json:"id"`
	Prompt    string   `json:"prompt"`
	Checkers  []Check  `json:"checkers"`
	MaxTurns  int      `json:"max_turns,omitempty"`  // reserved for multi-turn suites
	TimeoutS  int      `json:"timeout_seconds,omitempty"`
	Seeds     []int64  `json:"seeds,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	SessionID string   `json:"-"`
}

// Check is one checker invocation.
type Check struct {
	Type     string          `json:"type"` // exact_file | file_contains | exit_zero | llm_judge
	File     string          `json:"file,omitempty"`
	Hash     string          `json:"sha256,omitempty"`
	Pattern  string          `json:"pattern,omitempty"`
	Files    []string        `json:"files,omitempty"`
	Command  []string        `json:"command,omitempty"`
	Rubric   string          `json:"rubric,omitempty"`
	MinScore float64         `json:"min_score,omitempty"`
	Extra    json.RawMessage `json:"extra,omitempty"`
}

// Load reads and validates a suite manifest.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks required fields.
func (m *Manifest) Validate() error {
	if m.Suite == "" {
		return fmt.Errorf("suite name is required")
	}
	if len(m.Tasks) == 0 {
		return fmt.Errorf("suite must list at least one task")
	}
	for i := range m.Tasks {
		t := &m.Tasks[i]
		if t.ID == "" {
			return fmt.Errorf("task[%d]: id is required", i)
		}
		if t.Prompt == "" {
			return fmt.Errorf("task %s: prompt is required", t.ID)
		}
		if len(t.Checkers) == 0 {
			return fmt.Errorf("task %s: at least one checker is required", t.ID)
		}
		for j, c := range t.Checkers {
			switch c.Type {
			case "exact_file":
				if c.File == "" || c.Hash == "" {
					return fmt.Errorf("task %s check[%d]: exact_file needs file+sha256", t.ID, j)
				}
			case "file_contains":
				if len(c.Files) == 0 && c.File == "" {
					return fmt.Errorf("task %s check[%d]: file_contains needs files", t.ID, j)
				}
				if c.Pattern == "" {
					return fmt.Errorf("task %s check[%d]: file_contains needs pattern", t.ID, j)
				}
			case "exit_zero":
				if len(c.Command) == 0 {
					return fmt.Errorf("task %s check[%d]: exit_zero needs command", t.ID, j)
				}
			case "llm_judge":
				if c.Rubric == "" {
					return fmt.Errorf("task %s check[%d]: llm_judge needs rubric", t.ID, j)
				}
			default:
				return fmt.Errorf("task %s check[%d]: unknown checker type %q", t.ID, j, c.Type)
			}
		}
	}
	return nil
}

// Timeout returns the effective per-task timeout.
func (t *Task) Timeout() time.Duration {
	if t.TimeoutS <= 0 {
		return 10 * time.Minute
	}
	return time.Duration(t.TimeoutS) * time.Second
}

// Select filters tasks by ID substring (empty filter = all).
func (m *Manifest) Select(filter string) []Task {
	if filter == "" {
		return m.Tasks
	}
	var out []Task
	for _, t := range m.Tasks {
		if contains(t.ID, filter) {
			out = append(out, t)
		}
	}
	return out
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
