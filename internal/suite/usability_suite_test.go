package suite

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

// Gate T3 (07-bench/00T), suite half: pin the HARDENED usability suite
// BEFORE implementation (07-bench/01 modifies suites/usability-probe.json).
// Contract under test:
//
//   - suites/usability-probe.json loads clean, >= 6 tasks, unique IDs.
//   - NO vacuous checker anywhere: exit_zero ["true"] (or shell-wrapped
//     true/:) passes unconditionally and hides the real outcome (I8: the
//     u-plan-mode-request PASS in the baseline was vacuous).
//   - Hardened Validate REJECTS a vacuous exit_zero(true) at load time.
//   - Validate ACCEPTS the new acceptance checker types terminal_delivery
//     and plan_artifact (registered by 07-bench/01).
//   - Every task carries a terminal_delivery checker (R6: every submit
//     ends in a non-parked terminal or explicit actionable rejection).
//   - u-plan-mode-request carries a REAL plan checker: plan_artifact or
//     llm_judge, never exit_zero(true).
//   - Prompts and scheduled turn messages are FROZEN verbatim: the pins
//     below are byte-identical to the 2026-11-19 frozen fixtures. Never
//     reword a prompt to make a checker pass.
//
// RED reasons until 07-bench/01 lands: u-plan-mode-request still carries
// the vacuous exit_zero(true); no task carries terminal_delivery; Validate
// rejects terminal_delivery/plan_artifact as unknown types and accepts
// vacuous exit_zero(true).

// frozenUsabilityPrompts are the frozen primary prompts, verbatim.
var frozenUsabilityPrompts = map[string]string{
	"u-context-two-step":      "Create a file named counter.txt in the repository root containing exactly the number 3 on a single line. Use file_write with direct:true.",
	"u-steer-midrun":          "Create a file named result.txt in the repository root containing exactly the number 7 on a single line. Use file_write with direct:true so the file lands on disk immediately.",
	"u-route-research-write":  "Fetch the page at http://127.0.0.1:9/usability-fixture.html and save its body text to a file named fetched.txt in the repository root.",
	"u-fail-nonexistent-read": "Read the file definitely_missing_12345.txt in the repository root and tell me its exact contents. If you cannot read it, tell me plainly that it does not exist.",
	"u-plan-mode-request":     "Make a plan for adding a greetings module to this project: it should say hello in three languages, have tests, and a README section. Do not implement anything yet, just produce the plan for my approval.",
	"u-direct-explicit":       "Write a file named direct.txt containing the word ok. Do this directly now, no planning needed. Use file_write with direct:true.",
}

// frozenUsabilityTurns are the frozen scheduled turn messages, verbatim.
var frozenUsabilityTurns = map[string][]string{
	"u-context-two-step": {
		"Read counter.txt, add 4 to the number in it, and write the result back to counter.txt (file_write with direct:true). Then reply with the new number.",
	},
	"u-steer-midrun": {
		"Create a file named result2.txt in the repository root containing exactly the number 7 on a single line. Use file_write with direct:true so result2.txt lands on disk immediately. This replaces the earlier result.txt - after this, the only result file on disk must be result2.txt. Do not start new work beyond writing result2.txt.",
	},
}

// isVacuousExitZero reports whether a check passes unconditionally.
func isVacuousExitZero(c Check) bool {
	if c.Type != "exit_zero" {
		return false
	}
	switch strings.Join(c.Command, " ") {
	case "true", "sh -c true", "sh -c :", "bash -c true", "/usr/bin/true":
		return true
	}
	return false
}

func usabilitySuitePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return strings.TrimSuffix(thisFile, "/internal/suite/usability_suite_test.go") + "/suites/usability-probe.json"
}

func TestSuiteLoadsUsabilityProbe(t *testing.T) {
	path := usabilitySuitePath(t)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("loading %s: %v", path, err)
	}
	if m.Suite != "usability" {
		t.Errorf("suite name = %q, want %q", m.Suite, "usability")
	}
	if len(m.Tasks) < 6 {
		t.Fatalf("task count = %d, want >= 6", len(m.Tasks))
	}

	seen := make(map[string]bool, len(m.Tasks))
	for i, task := range m.Tasks {
		if seen[task.ID] {
			t.Errorf("task[%d]: duplicate id %q", i, task.ID)
		}
		seen[task.ID] = true
		if task.TimeoutS <= 0 {
			t.Errorf("task %s: timeout_seconds not set", task.ID)
		}
		if len(task.Seeds) == 0 {
			t.Errorf("task %s: seeds not set", task.ID)
		}
		if !hasTag(task, "usability") {
			t.Errorf("task %s: missing usability tag", task.ID)
		}

		// Frozen prompts: byte-identical, never reworded.
		want, known := frozenUsabilityPrompts[task.ID]
		if !known {
			t.Errorf("task %s: not in the frozen fixture set", task.ID)
			continue
		}
		if task.Prompt != want {
			t.Errorf("task %s: prompt drift (prompts are FROZEN):\n got: %q\nwant: %q", task.ID, task.Prompt, want)
		}
		wantTurns := frozenUsabilityTurns[task.ID]
		if len(task.Turns) != len(wantTurns) {
			t.Errorf("task %s: turn count = %d, want %d (frozen)", task.ID, len(task.Turns), len(wantTurns))
		} else {
			for j, msg := range wantTurns {
				if task.Turns[j].Message != msg {
					t.Errorf("task %s turn[%d]: message drift (FROZEN):\n got: %q\nwant: %q", task.ID, j, task.Turns[j].Message, msg)
				}
			}
		}

		// No vacuous checkers anywhere.
		for j, c := range task.Checkers {
			if isVacuousExitZero(c) {
				t.Errorf("RED: task %s check[%d] is a vacuous exit_zero(true); replace with a real outcome checker (I8)", task.ID, j)
			}
		}

		// R6: every task asserts terminal delivery.
		hasTerminal := false
		for _, c := range task.Checkers {
			if c.Type == "terminal_delivery" {
				hasTerminal = true
			}
		}
		if !hasTerminal {
			t.Errorf("RED: task %s has no terminal_delivery checker (R6 acceptance pin)", task.ID)
		}
	}

	// The plan task must carry a REAL plan checker.
	var planTask *Task
	for i := range m.Tasks {
		if m.Tasks[i].ID == "u-plan-mode-request" {
			planTask = &m.Tasks[i]
		}
	}
	if planTask == nil {
		t.Fatal("u-plan-mode-request missing from the suite")
	}
	realPlanCheck := false
	for _, c := range planTask.Checkers {
		if c.Type == "plan_artifact" || c.Type == "llm_judge" {
			realPlanCheck = true
		}
	}
	if !realPlanCheck {
		t.Errorf("RED: u-plan-mode-request has no plan_artifact/llm_judge checker (vacuous exit_zero(true) must be replaced)")
	}
}

func TestSuiteLoadsValidateAcceptsAcceptanceCheckers(t *testing.T) {
	// Hardened Validate must accept the new acceptance checker types.
	for _, typ := range []string{"terminal_delivery", "plan_artifact"} {
		m := &Manifest{
			Suite: "gate",
			Tasks: []Task{{
				ID: "t1", Prompt: "do the thing",
				Checkers: []Check{{Type: typ, Extra: json.RawMessage(`{"task_id": "t1"}`)}},
			}},
		}
		if err := m.Validate(); err != nil {
			t.Errorf("RED: Validate rejects acceptance checker type %q: %v", typ, err)
		}
	}
}

func TestSuiteLoadsValidateRejectsVacuousChecker(t *testing.T) {
	// Hardened loading: a vacuous exit_zero(true) must be rejected at
	// load time, before any model call is burned.
	for _, cmd := range [][]string{{"true"}, {"sh", "-c", "true"}, {"sh", "-c", ":"}} {
		m := &Manifest{
			Suite: "gate",
			Tasks: []Task{{
				ID: "t1", Prompt: "do the thing",
				Checkers: []Check{{Type: "exit_zero", Command: cmd}},
			}},
		}
		if err := m.Validate(); err == nil {
			t.Errorf("RED: Validate accepts vacuous exit_zero %v; it must refuse unconditional checkers", cmd)
		}
	}
}
