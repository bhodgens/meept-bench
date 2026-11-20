package checkers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bhodgens/meept-bench/internal/results"
	"github.com/bhodgens/meept-bench/internal/suite"
)

// Gate T1 (07-bench/00T): pin the terminal_delivery acceptance checker
// BEFORE implementation (07-bench/01). Contract under test:
//
//   - checker type "terminal_delivery" registered in checkers.Run.
//   - reads ONLY the current run's transcript dir (WithTranscriptDir option,
//     else MEEPT_BENCH_TRANSCRIPT_DIR env); never a bare results/** glob.
//   - transcript association via extra.task_id (the sanctioned Check
//     extension channel); missing association fails, never passes.
//   - every submitted turn (the primary prompt plus each scheduled turn
//     with accepted:true) needs a turn.terminal bus event captured in the
//     transcript tool_trace, carrying the C1 closed-set status
//     (completed|failed|timeout|parked) and a non-empty turn_id, arriving
//     within extra.deadline_seconds (default 600) of transcript start.
//   - parked terminals never satisfy delivery; completed/failed/timeout do
//     (delivery is graded, not work success: an honest failed/timeout
//     terminal is a delivered terminal).
//   - an explicit actionable rejection (TurnEvent accepted:false with a
//     non-empty note, C4) satisfies its turn and is surfaced in the detail.
//     A silent accepted:false (no note) is NOT actionable and fails.
//   - satisfying terminals are counted by DISTINCT turn_id: one terminal
//     can never cover two submitted turns (no assumed chain credit).
//
// MUTATION DISCIPLINE (load-bearing acceptance pin): the final subtest
// builds a passing fixture, proves it passes, then removes the terminal
// event from the SAME fixture and proves the checker flips to FAIL.

func usabilityBaseTime() time.Time {
	return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
}

// writeUsabilityTranscript writes one current-run transcript fixture into
// dir using the runner's real filename convention (<suite>-<task>-a<N>.json).
func writeUsabilityTranscript(t *testing.T, dir, taskID string, tr *results.Transcript) {
	t.Helper()
	tr.Suite = "usability"
	tr.TaskID = taskID
	if tr.Attempt == 0 {
		tr.Attempt = 1
	}
	data, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal transcript fixture: %v", err)
	}
	name := "usability-" + taskID + "-a1.json"
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("write transcript fixture: %v", err)
	}
}

// terminalEvent builds a turn.terminal trace event with the frozen C1 keys.
func terminalEvent(t *testing.T, at time.Time, turnID, status string) results.ToolEvent {
	t.Helper()
	payload := map[string]any{
		"conversation_id": "conv-gate",
		"session_id":      "sess-gate",
		"turn_id":         turnID,
		"task_id":         "task-gate",
		"status":          status,
		"reply":           "done",
		"duration_ms":     1000,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal terminal payload: %v", err)
	}
	return results.ToolEvent{At: at, Topic: "turn.terminal", Type: "event", Source: "gate", Raw: raw}
}

// progressEvent is a non-terminal trace event (noise the checker must ignore).
func progressEvent(at time.Time) results.ToolEvent {
	return results.ToolEvent{At: at, Topic: "tool.execution.progress", Type: "event", Source: "gate", Raw: json.RawMessage(`{"tool":"file_write"}`)}
}

func tdExtra(t *testing.T, taskID string, deadline int) map[string]any {
	t.Helper()
	m := map[string]any{"task_id": taskID}
	if deadline > 0 {
		m["deadline_seconds"] = deadline
	}
	return m
}

func tdRun(t *testing.T, ctx context.Context, dir string, extra map[string]any) Result {
	t.Helper()
	raw, err := json.Marshal(extra)
	if err != nil {
		t.Fatalf("marshal extra: %v", err)
	}
	c := suite.Check{Type: "terminal_delivery", Extra: raw}
	return Run(ctx, c, t.TempDir(), "final reply", nil, WithTranscriptDir(dir))
}

func TestTerminalDeliveryChecker(t *testing.T) {
	ctx := context.Background()
	base := usabilityBaseTime()

	// Recognition guard: the checker type must exist before any behavioral
	// subtest runs, so a missing implementation is a single clean RED
	// (not a pile of accidentally-green failure assertions).
	guardDir := t.TempDir()
	writeUsabilityTranscript(t, guardDir, "u-term", &results.Transcript{
		Prompt:    "do the thing",
		StartedAt: base,
		EndedAt:   base.Add(30 * time.Second),
		ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "turn-1", "completed")},
	})
	guard := tdRun(t, ctx, guardDir, tdExtra(t, "u-term", 300))
	if guard.Detail == "unknown checker type" {
		t.Fatalf("RED: terminal_delivery checker is not implemented (Run detail: %q)", guard.Detail)
	}
	if !guard.Passed {
		t.Fatalf("passing fixture must pass: %+v", guard)
	}

	t.Run("missing_terminal_fails", func(t *testing.T) {
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(30 * time.Second),
			ToolTrace: []results.ToolEvent{progressEvent(base.Add(5 * time.Second))},
		})
		res := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300))
		if res.Passed {
			t.Fatalf("submitted turn without a terminal must FAIL: %+v", res)
		}
		if !strings.Contains(strings.ToLower(res.Detail), "terminal") {
			t.Fatalf("failure detail must name the missing terminal, got %q", res.Detail)
		}
	})

	t.Run("parked_only_fails", func(t *testing.T) {
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(30 * time.Second),
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "turn-1", "parked")},
		})
		res := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300))
		if res.Passed {
			t.Fatalf("parked-only turn must FAIL (park is not delivery): %+v", res)
		}
	})

	t.Run("failed_terminal_passes", func(t *testing.T) {
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(30 * time.Second),
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "turn-1", "failed")},
		})
		if res := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300)); !res.Passed {
			t.Fatalf("honest failed terminal is delivery and must PASS: %+v", res)
		}
	})

	t.Run("timeout_terminal_passes", func(t *testing.T) {
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(30 * time.Second),
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "turn-1", "timeout")},
		})
		if res := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300)); !res.Passed {
			t.Fatalf("honest timeout terminal is delivery and must PASS: %+v", res)
		}
	})

	t.Run("parked_then_completed_passes", func(t *testing.T) {
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(90 * time.Second),
			ToolTrace: []results.ToolEvent{
				terminalEvent(t, base.Add(20*time.Second), "turn-1", "parked"),
				terminalEvent(t, base.Add(80*time.Second), "turn-1", "completed"),
			},
		})
		if res := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300)); !res.Passed {
			t.Fatalf("park then completed for one turn must PASS: %+v", res)
		}
	})

	t.Run("late_terminal_fails", func(t *testing.T) {
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(410 * time.Second),
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(400*time.Second), "turn-1", "completed")},
		})
		if res := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300)); res.Passed {
			t.Fatalf("terminal after the deadline must FAIL: %+v", res)
		}
	})

	t.Run("default_deadline_fails_late_terminal", func(t *testing.T) {
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(710 * time.Second),
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(700*time.Second), "turn-1", "completed")},
		})
		// No deadline_seconds: default 600s applies.
		if res := tdRun(t, ctx, dir, tdExtra(t, "u-term", 0)); res.Passed {
			t.Fatalf("terminal beyond the default 600s deadline must FAIL: %+v", res)
		}
	})

	t.Run("c1_status_set_enforced", func(t *testing.T) {
		for _, status := range []string{"done", "ok", ""} {
			dir := t.TempDir()
			writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
				Prompt:    "do the thing",
				StartedAt: base,
				EndedAt:   base.Add(30 * time.Second),
				ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "turn-1", status)},
			})
			if res := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300)); res.Passed {
				t.Fatalf("status %q outside the C1 closed set must FAIL: %+v", status, res)
			}
		}
	})

	t.Run("missing_turn_id_fails", func(t *testing.T) {
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(30 * time.Second),
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "", "completed")},
		})
		if res := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300)); res.Passed {
			t.Fatalf("terminal without turn_id must FAIL (no assumed credit): %+v", res)
		}
	})

	acceptedTurn := results.TurnEvent{
		Message: "follow up", DelayS: 5, SentAt: base.Add(5 * time.Second),
		PathUsed: "steer", Accepted: true,
	}

	t.Run("two_turns_need_two_distinct_terminals", func(t *testing.T) {
		// One terminal for two submitted turns: FAIL.
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(60 * time.Second),
			Turns:     []results.TurnEvent{acceptedTurn},
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "turn-1", "completed")},
		})
		if res := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300)); res.Passed {
			t.Fatalf("one terminal cannot cover two submitted turns: %+v", res)
		}

		// Duplicate turn_id twice: still one distinct terminal: FAIL.
		dir2 := t.TempDir()
		writeUsabilityTranscript(t, dir2, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(60 * time.Second),
			Turns:     []results.TurnEvent{acceptedTurn},
			ToolTrace: []results.ToolEvent{
				terminalEvent(t, base.Add(20*time.Second), "turn-1", "completed"),
				terminalEvent(t, base.Add(40*time.Second), "turn-1", "completed"),
			},
		})
		if res := tdRun(t, ctx, dir2, tdExtra(t, "u-term", 300)); res.Passed {
			t.Fatalf("duplicate turn_id must not count twice: %+v", res)
		}

		// Two distinct terminals: PASS.
		dir3 := t.TempDir()
		writeUsabilityTranscript(t, dir3, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(60 * time.Second),
			Turns:     []results.TurnEvent{acceptedTurn},
			ToolTrace: []results.ToolEvent{
				terminalEvent(t, base.Add(20*time.Second), "turn-1", "completed"),
				terminalEvent(t, base.Add(40*time.Second), "turn-2", "completed"),
			},
		})
		if res := tdRun(t, ctx, dir3, tdExtra(t, "u-term", 300)); !res.Passed {
			t.Fatalf("two distinct terminals for two turns must PASS: %+v", res)
		}
	})

	t.Run("actionable_rejection_passes_with_note", func(t *testing.T) {
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(30 * time.Second),
			Turns: []results.TurnEvent{{
				Message: "follow up", DelayS: 5, SentAt: base.Add(5 * time.Second),
				PathUsed: "chat", Accepted: false,
				Error: "unknown conversation_id; create the session first",
			}},
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "turn-1", "completed")},
		})
		res := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300))
		if !res.Passed {
			t.Fatalf("explicit actionable rejection (accepted:false + note) must PASS: %+v", res)
		}
		if !strings.Contains(strings.ToLower(res.Detail), "rejection") {
			t.Fatalf("pass detail must surface the rejection note, got %q", res.Detail)
		}
	})

	t.Run("silent_rejection_fails", func(t *testing.T) {
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(30 * time.Second),
			Turns: []results.TurnEvent{{
				Message: "follow up", DelayS: 5, SentAt: base.Add(5 * time.Second),
				PathUsed: "chat", Accepted: false,
			}},
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "turn-1", "completed")},
		})
		if res := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300)); res.Passed {
			t.Fatalf("accepted:false without an actionable note must FAIL: %+v", res)
		}
	})

	t.Run("env_fallback_when_no_option", func(t *testing.T) {
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(30 * time.Second),
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "turn-1", "completed")},
		})
		t.Setenv("MEEPT_BENCH_TRANSCRIPT_DIR", dir)
		raw, err := json.Marshal(tdExtra(t, "u-term", 300))
		if err != nil {
			t.Fatal(err)
		}
		c := suite.Check{Type: "terminal_delivery", Extra: raw}
		if res := Run(ctx, c, t.TempDir(), "final reply", nil); !res.Passed {
			t.Fatalf("env MEEPT_BENCH_TRANSCRIPT_DIR must be honored without the option: %+v", res)
		}
	})

	t.Run("no_dir_no_env_fails", func(t *testing.T) {
		t.Setenv("MEEPT_BENCH_TRANSCRIPT_DIR", "")
		raw, err := json.Marshal(tdExtra(t, "u-term", 300))
		if err != nil {
			t.Fatal(err)
		}
		c := suite.Check{Type: "terminal_delivery", Extra: raw}
		if res := Run(ctx, c, t.TempDir(), "final reply", nil); res.Passed {
			t.Fatalf("no transcript dir and no env must FAIL, never pass: %+v", res)
		}
	})

	t.Run("never_globs_stale_results", func(t *testing.T) {
		// Decoy: a PASSING fixture for the same task sits under a bare
		// results/** tree in the CWD. The current-run dir holds a
		// parked-only (failing) fixture. A checker that globs results/**
		// would pick the decoy and pass; it must read ONLY the given dir.
		decoyRoot := t.TempDir()
		decoyDir := filepath.Join(decoyRoot, "results", "usability", "transcripts")
		if err := os.MkdirAll(decoyDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeUsabilityTranscript(t, decoyDir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(30 * time.Second),
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "turn-1", "completed")},
		})
		t.Chdir(decoyRoot)

		currentDir := t.TempDir()
		writeUsabilityTranscript(t, currentDir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(30 * time.Second),
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "turn-1", "parked")},
		})
		if res := tdRun(t, ctx, currentDir, tdExtra(t, "u-term", 300)); res.Passed {
			t.Fatalf("checker must read only the current-run dir, not results/**: %+v", res)
		}
	})

	t.Run("missing_task_association_fails", func(t *testing.T) {
		dir := t.TempDir()
		writeUsabilityTranscript(t, dir, "u-term", &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(30 * time.Second),
			ToolTrace: []results.ToolEvent{terminalEvent(t, base.Add(20*time.Second), "turn-1", "completed")},
		})
		if res := tdRun(t, ctx, dir, map[string]any{}); res.Passed {
			t.Fatalf("missing extra.task_id must FAIL (no association, no credit): %+v", res)
		}
		if res := tdRun(t, ctx, dir, tdExtra(t, "u-other", 300)); res.Passed {
			t.Fatalf("transcript for a different task must FAIL: %+v", res)
		}
	})

	// MUTATION VERIFICATION: the load-bearing acceptance pin. A passing
	// fixture must flip to FAIL when its terminal event is removed.
	t.Run("mutation_remove_terminal_flips_to_fail", func(t *testing.T) {
		dir := t.TempDir()
		tr := &results.Transcript{
			Prompt:    "do the thing",
			StartedAt: base,
			EndedAt:   base.Add(30 * time.Second),
			ToolTrace: []results.ToolEvent{
				progressEvent(base.Add(5 * time.Second)),
				terminalEvent(t, base.Add(20*time.Second), "turn-1", "completed"),
			},
		}
		writeUsabilityTranscript(t, dir, "u-term", tr)
		before := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300))
		if !before.Passed {
			t.Fatalf("mutation baseline must PASS before removal: %+v", before)
		}

		// Remove ONLY the terminal event; keep the rest of the fixture.
		var kept []results.ToolEvent
		for _, ev := range tr.ToolTrace {
			if ev.Topic != "turn.terminal" {
				kept = append(kept, ev)
			}
		}
		tr.ToolTrace = kept
		writeUsabilityTranscript(t, dir, "u-term", tr)
		after := tdRun(t, ctx, dir, tdExtra(t, "u-term", 300))
		if after.Passed {
			t.Fatalf("MUTATION NOT DETECTED: removing the terminal must flip the checker to FAIL: %+v", after)
		}
	})
}
