package checkers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bhodgens/meept-bench/internal/results"
	"github.com/bhodgens/meept-bench/internal/suite"
)

// terminal_delivery is an outcome-level acceptance checker (plan
// 20261119-usability-remediation, I8/R6): every submitted turn ends in a
// non-parked turn.terminal bus event (the C1 closed set) within the task
// deadline, or in an explicit actionable rejection.
//
// Delivery is graded, not work success: an honest failed/timeout terminal
// is a delivered terminal (the daemon surfaced the outcome); a parked turn
// is never delivery.
//
// The checker reads ONLY the current run's transcript dir (WithTranscriptDir
// option, else MEEPT_BENCH_TRANSCRIPT_DIR) -- never a bare results/** glob,
// which leaks stale runs from sick-daemon gates into later verdicts.
//
// Counting is by DISTINCT turn_id: one terminal event can never cover two
// submitted turns (no assumed chain credit).
const terminalDeliveryType = "terminal_delivery"

// c1TerminalStatuses is the frozen C1 closed set of turn.terminal statuses.
var c1TerminalStatuses = map[string]bool{
	"completed": true,
	"failed":    true,
	"timeout":   true,
	// "parked" is deliberately absent: park is not delivery.
}

// defaultTerminalDeadlineS is the per-turn delivery deadline applied when
// the check's extra carries no deadline_seconds.
const defaultTerminalDeadlineS = 600

// runTerminalDelivery implements the terminal_delivery checker.
func runTerminalDelivery(ctx context.Context, c suite.Check, rc runConfig) (bool, string, error) {
	dir, err := transcriptDirFor(rc)
	if err != nil {
		return false, "", err
	}
	taskID := extraString(c.Extra, "task_id")
	if taskID == "" {
		return false, "", fmt.Errorf("terminal_delivery: check extra.task_id is required (no association, no credit)")
	}
	deadlineS := defaultTerminalDeadlineS
	if v := extraFloat(c.Extra, "deadline_seconds"); v > 0 {
		deadlineS = int(v)
	}
	deadline := time.Duration(deadlineS) * time.Second

	tr, err := loadTaskTranscript(dir, taskID)
	if err != nil {
		return false, "", err
	}

	// Submitted turns = the primary prompt + each scheduled turn the daemon
	// ACCEPTED (accepted:true). A rejected turn is only satisfied by an
	// explicit actionable rejection (accepted:false + non-empty note, C4).
	type submitted struct {
		label    string
		rejected bool // accepted:false
		note     string
		hasTerm  bool
		termID   string
		termStat string
		termAt   time.Time
		reason   string
	}
	submissions := make([]submitted, 0, 1+len(tr.Turns))
	submissions = append(submissions, submitted{label: "primary"})

	for i, te := range tr.Turns {
		s := submitted{label: fmt.Sprintf("turn[%d]", i)}
		if te.Accepted {
			s.label += " (steer/followup accepted)"
		} else {
			s.rejected = true
			s.note = te.Error
		}
		submissions = append(submissions, s)
	}

	// Collect terminal events (non-parked, in-set status, non-empty turn_id,
	// within deadline), deduplicated by turn_id: one terminal covers one
	// submitted turn, never two.
	type terminal struct {
		turnID string
		status string
		at     time.Time
	}
	var terminals []terminal
	seenTurnIDs := make(map[string]bool)
	for _, ev := range tr.ToolTrace {
		if ev.Topic != "turn.terminal" {
			continue
		}
		var p struct {
			TurnID string    `json:"turn_id"`
			Status string    `json:"status"`
			At     time.Time `json:"at"`
			Parked bool      `json:"parked"`
		}
		if err := json.Unmarshal(ev.Raw, &p); err != nil {
			continue
		}
		status := p.Status
		if status == "" {
			status = strings.TrimSpace(string(ev.Raw))
		}
		if !c1TerminalStatuses[status] {
			continue // parked or outside the C1 set: not delivery
		}
		if p.TurnID == "" {
			continue // no turn_id: no assumed credit
		}
		// Deadline measured from transcript start.
		if ev.At.After(tr.StartedAt.Add(deadline)) || ev.At.Equal(tr.StartedAt.Add(deadline)) {
			continue
		}
		if seenTurnIDs[p.TurnID] {
			continue // duplicate turn_id: still one distinct terminal
		}
		seenTurnIDs[p.TurnID] = true
		terminals = append(terminals, terminal{turnID: p.TurnID, status: status, at: ev.At})
	}

	// Assignment: submitted turns consume distinct terminals in order.
	free := make(map[int]bool, len(terminals)) // index into terminals
	for i := range terminals {
		free[i] = true
	}

	var missing []string
	var notes []string
	usedDistinct := 0
	for si := range submissions {
		s := &submissions[si]
		if s.rejected {
			if strings.TrimSpace(s.note) != "" {
				notes = append(notes, fmt.Sprintf("%s: explicit actionable rejection (accepted:false): %q", s.label, s.note))
				continue
			}
			missing = append(missing, fmt.Sprintf("%s: silently rejected (accepted:false, no actionable note)", s.label))
			continue
		}
		matched := -1
		for i := range terminals {
			if free[i] {
				matched = i
				break
			}
		}
		if matched >= 0 {
			free[matched] = false
			usedDistinct++
			t := terminals[matched]
			notes = append(notes, fmt.Sprintf("%s: terminal turn_id=%s status=%s at+%s", s.label, t.turnID, t.status, t.at.Sub(tr.StartedAt).Round(time.Millisecond)))
			continue
		}
		missing = append(missing, fmt.Sprintf("%s: no non-parked turn.terminal within %ds of transcript start", s.label, deadlineS))
	}

	detail := fmt.Sprintf("terminal_delivery: %d/%d submitted turns delivered (%d distinct terminals)", usedDistinct, len(submissions), len(terminals))
	if len(notes) > 0 {
		detail += "; " + strings.Join(notes, "; ")
	}
	if len(missing) > 0 {
		return false, detail + "; missing: " + strings.Join(missing, "; "), nil
	}
	// Actionable-rejection turns must be surfaced in the pass detail too;
	// the join above already carries them via notes.
	return true, detail, nil
}

// transcriptDirFor resolves the current-run transcript dir: option first,
// then MEEPT_BENCH_TRANSCRIPT_DIR. Never a results/** glob.
func transcriptDirFor(rc runConfig) (string, error) {
	dir := rc.transcriptDir
	if dir == "" {
		dir = os.Getenv("MEEPT_BENCH_TRANSCRIPT_DIR")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", fmt.Errorf("no current-run transcript dir (WithTranscriptDir option or MEEPT_BENCH_TRANSCRIPT_DIR); refusing to glob results/**")
	}
	return dir, nil
}

// loadTaskTranscript reads the current-run transcript for one task using
// the runner's filename convention (<suite>-<task>-a<N>.json). A missing
// transcript fails; the checker never passes on missing association.
func loadTaskTranscript(dir, taskID string) (*results.Transcript, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*-"+taskID+"-a*.json"))
	if err != nil || len(matches) == 0 {
		return nil, fmt.Errorf("no current-run transcript for task %q in %s", taskID, dir)
	}
	sort.Strings(matches) // attempt order; newest attempt last
	data, err := os.ReadFile(matches[len(matches)-1])
	if err != nil {
		return nil, fmt.Errorf("read transcript for task %q: %w", taskID, err)
	}
	var tr results.Transcript
	if err := json.Unmarshal(data, &tr); err != nil {
		return nil, fmt.Errorf("parse transcript for task %q: %w", taskID, err)
	}
	if tr.TaskID != taskID {
		return nil, fmt.Errorf("transcript %s carries task_id %q, want %q (association failure)", filepath.Base(matches[len(matches)-1]), tr.TaskID, taskID)
	}
	return &tr, nil
}

// extraString reads a string field from a check's extra object.
func extraString(extra json.RawMessage, key string) string {
	if len(extra) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(extra, &m); err != nil {
		return ""
	}
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

// extraFloat reads a numeric field from a check's extra object.
func extraFloat(extra json.RawMessage, key string) float64 {
	if len(extra) == 0 {
		return 0
	}
	var m map[string]any
	if err := json.Unmarshal(extra, &m); err != nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return v
	case string:
		var f float64
		_, _ = fmt.Sscanf(v, "%g", &f)
		return f
	default:
		return 0
	}
}
