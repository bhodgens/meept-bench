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

// Gate T3 (07-bench/00T), checker half: pin the plan_artifact acceptance
// checker BEFORE implementation (07-bench/01 internal/checkers/plan_artifact.go).
// Contract under test:
//
//   - checker type "plan_artifact" registered in checkers.Run.
//   - PASS evidence (any one):
//     (a) a real plan artifact in the task worktree: non-empty plan.md or
//     PLAN.md at the root, or a non-empty *.md under plans/;
//     (b) plan-draft lifecycle evidence in the current-run transcript's
//     tool_trace: a plan.created / plan.draft / plan.sealed /
//     plan.submitting bus event;
//     (c) a judge-verified plan reply: check carries a rubric and the
//     configured judge scores the final reply >= min_score.
//   - FAIL when no plan artifact/draft was produced: reply prose alone
//     (even plan-shaped text or a step-log dump, the I8 baseline shape)
//     never passes without one of the evidence paths above - no assumed
//     chain credit. Empty plan files do not count.
//   - reads ONLY the current run's transcript dir (WithTranscriptDir
//     option, else MEEPT_BENCH_TRANSCRIPT_DIR env); never results/**.
//   - transcript association via extra.task_id; missing association
//     fails, never passes.
//
// RED reason until 07-bench/01 lands: Run returns "unknown checker type"
// for plan_artifact (guard below).

// writePlanTranscript writes a current-run transcript fixture for the plan
// task, optionally carrying plan lifecycle trace events.
func writePlanTranscript(t *testing.T, dir string, trace []results.ToolEvent) {
	t.Helper()
	base := usabilityBaseTime()
	tr := &results.Transcript{
		Prompt:     "Make a plan for adding a greetings module to this project: it should say hello in three languages, have tests, and a README section. Do not implement anything yet, just produce the plan for my approval.",
		FinalReply: "Here is the plan for your approval.",
		StartedAt:  base,
		EndedAt:    base.Add(30 * time.Second),
		ToolTrace:  trace,
	}
	writeUsabilityTranscript(t, dir, "u-plan-mode-request", tr)
}

func planBusEvent(at time.Time, topic string) results.ToolEvent {
	return results.ToolEvent{
		At: at, Topic: topic, Type: "event", Source: "gate",
		Raw: json.RawMessage(`{"plan_id":"plan-1","session_id":"sess-gate"}`),
	}
}

func paRun(t *testing.T, ctx context.Context, dir, worktree, reply string, judge Judge, withRubric bool) Result {
	t.Helper()
	c := suite.Check{
		Type:  "plan_artifact",
		Extra: json.RawMessage(`{"task_id": "u-plan-mode-request"}`),
	}
	if withRubric {
		c.Rubric = "Score whether the reply presents a concrete multi-step plan for approval: steps, tests, and documentation coverage. Score 0 for step-log dumps, job-ID prose, or implementation chatter."
	}
	return Run(ctx, c, worktree, reply, judge, WithTranscriptDir(dir))
}

func TestPlanArtifactChecker(t *testing.T) {
	ctx := context.Background()
	base := usabilityBaseTime()

	// Recognition guard: missing implementation is one clean RED.
	guardDir := t.TempDir()
	writePlanTranscript(t, guardDir, nil)
	guardWt := t.TempDir()
	if err := os.WriteFile(filepath.Join(guardWt, "plan.md"), []byte("# Greetings module plan\n1. scaffold\n2. tests\n3. README\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guard := paRun(t, ctx, guardDir, guardWt, "Here is the plan.", nil, false)
	if guard.Detail == "unknown checker type" {
		t.Fatalf("RED: plan_artifact checker is not implemented (Run detail: %q)", guard.Detail)
	}
	if !guard.Passed {
		t.Fatalf("plan.md artifact fixture must PASS: %+v", guard)
	}

	t.Run("no_artifact_no_draft_fails", func(t *testing.T) {
		dir := t.TempDir()
		writePlanTranscript(t, dir, nil)
		wt := t.TempDir()
		// The I8 baseline shape: task "completed", reply is a step-log
		// dump, no plan artifact anywhere. Must FAIL.
		reply := "## review:\nstep 1 approved\nstep 2 approved\nTask task-9f3a2b completed."
		res := paRun(t, ctx, dir, wt, reply, nil, false)
		if res.Passed {
			t.Fatalf("no plan artifact/draft produced must FAIL: %+v", res)
		}
		if !strings.Contains(strings.ToLower(res.Detail), "plan") {
			t.Fatalf("failure detail must name the missing plan artifact, got %q", res.Detail)
		}
	})

	t.Run("plan_shaped_prose_alone_fails", func(t *testing.T) {
		dir := t.TempDir()
		writePlanTranscript(t, dir, nil)
		reply := "Plan:\n1. Add greetings module\n2. Write tests\n3. Update README\nAwaiting your approval."
		if res := paRun(t, ctx, dir, t.TempDir(), reply, nil, false); res.Passed {
			t.Fatalf("reply prose without artifact/draft/judge evidence must FAIL (no assumed credit): %+v", res)
		}
	})

	t.Run("empty_plan_file_fails", func(t *testing.T) {
		dir := t.TempDir()
		writePlanTranscript(t, dir, nil)
		wt := t.TempDir()
		if err := os.WriteFile(filepath.Join(wt, "plan.md"), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		if res := paRun(t, ctx, dir, wt, "Here is the plan.", nil, false); res.Passed {
			t.Fatalf("empty plan.md must FAIL: %+v", res)
		}
	})

	t.Run("unrelated_file_fails", func(t *testing.T) {
		dir := t.TempDir()
		writePlanTranscript(t, dir, nil)
		wt := t.TempDir()
		if err := os.WriteFile(filepath.Join(wt, "notes.txt"), []byte("some unrelated work"), 0o644); err != nil {
			t.Fatal(err)
		}
		if res := paRun(t, ctx, dir, wt, "Here is the plan.", nil, false); res.Passed {
			t.Fatalf("unrelated artifact must FAIL: %+v", res)
		}
	})

	t.Run("artifact_variants_pass", func(t *testing.T) {
		for _, rel := range []string{"plan.md", "PLAN.md", filepath.Join("plans", "greetings.md")} {
			dir := t.TempDir()
			writePlanTranscript(t, dir, nil)
			wt := t.TempDir()
			full := filepath.Join(wt, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("# Greetings module plan\n1. scaffold\n2. tests\n3. README\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if res := paRun(t, ctx, dir, wt, "Here is the plan.", nil, false); !res.Passed {
				t.Errorf("artifact %s must PASS: %+v", rel, res)
			}
		}
	})

	t.Run("draft_bus_evidence_passes", func(t *testing.T) {
		for _, topic := range []string{"plan.created", "plan.draft", "plan.sealed", "plan.submitting"} {
			dir := t.TempDir()
			writePlanTranscript(t, dir, []results.ToolEvent{planBusEvent(base.Add(10*time.Second), topic)})
			if res := paRun(t, ctx, dir, t.TempDir(), "Here is the plan.", nil, false); !res.Passed {
				t.Errorf("draft evidence topic %s must PASS: %+v", topic, res)
			}
		}
	})

	t.Run("judge_verified_reply_passes", func(t *testing.T) {
		dir := t.TempDir()
		writePlanTranscript(t, dir, nil)
		judge := judgeFunc(func(ctx context.Context, rubric, answer string) (float64, string, error) {
			return 0.9, "concrete plan presented for approval", nil
		})
		reply := "Plan:\n1. Add greetings module\n2. Write tests\n3. Update README\nAwaiting your approval."
		if res := paRun(t, ctx, dir, t.TempDir(), reply, judge, true); !res.Passed {
			t.Fatalf("judge-verified plan reply must PASS: %+v", res)
		}
	})

	t.Run("judge_verified_bad_reply_fails", func(t *testing.T) {
		dir := t.TempDir()
		writePlanTranscript(t, dir, nil)
		judge := judgeFunc(func(ctx context.Context, rubric, answer string) (float64, string, error) {
			return 0.1, "step-log dump, not a plan", nil
		})
		reply := "## review:\nstep 1 approved\nTask task-9f3a2b completed."
		if res := paRun(t, ctx, dir, t.TempDir(), reply, judge, true); res.Passed {
			t.Fatalf("low judge score must FAIL: %+v", res)
		}
	})

	t.Run("no_dir_no_env_fails", func(t *testing.T) {
		t.Setenv("MEEPT_BENCH_TRANSCRIPT_DIR", "")
		c := suite.Check{Type: "plan_artifact", Extra: json.RawMessage(`{"task_id": "u-plan-mode-request"}`)}
		wt := t.TempDir()
		if err := os.WriteFile(filepath.Join(wt, "plan.md"), []byte("# plan\nsteps\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Artifact evidence alone still needs a readable current-run
		// transcript for association; without any dir the checker must
		// fail closed, never pass.
		if res := Run(ctx, c, wt, "Here is the plan.", nil); res.Passed {
			t.Fatalf("no transcript dir and no env must FAIL, never pass: %+v", res)
		}
	})

	t.Run("missing_task_association_fails", func(t *testing.T) {
		dir := t.TempDir()
		writePlanTranscript(t, dir, nil)
		wt := t.TempDir()
		if err := os.WriteFile(filepath.Join(wt, "plan.md"), []byte("# plan\nsteps\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		c := suite.Check{Type: "plan_artifact", Extra: json.RawMessage(`{"task_id": "u-other-task"}`)}
		if res := Run(ctx, c, wt, "Here is the plan.", nil, WithTranscriptDir(dir)); res.Passed {
			t.Fatalf("transcript for a different task must FAIL: %+v", res)
		}
	})

	t.Run("never_globs_stale_results", func(t *testing.T) {
		// Decoy passing artifact+transcript under a bare results/** tree;
		// the current-run dir has no plan evidence. Must FAIL.
		decoyRoot := t.TempDir()
		decoyDir := filepath.Join(decoyRoot, "results", "usability", "transcripts")
		if err := os.MkdirAll(decoyDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writePlanTranscript(t, decoyDir, []results.ToolEvent{planBusEvent(base.Add(10*time.Second), "plan.created")})
		t.Chdir(decoyRoot)

		dir := t.TempDir()
		writePlanTranscript(t, dir, nil)
		if res := paRun(t, ctx, dir, t.TempDir(), "Here is the plan.", nil, false); res.Passed {
			t.Fatalf("checker must read only the current-run dir, not results/**: %+v", res)
		}
	})

	// Secondary mutation: removing the plan artifact from a passing
	// fixture must flip the checker to FAIL.
	t.Run("mutation_remove_artifact_flips_to_fail", func(t *testing.T) {
		dir := t.TempDir()
		writePlanTranscript(t, dir, nil)
		wt := t.TempDir()
		artifact := filepath.Join(wt, "plan.md")
		if err := os.WriteFile(artifact, []byte("# Greetings module plan\n1. scaffold\n2. tests\n3. README\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if res := paRun(t, ctx, dir, wt, "Here is the plan.", nil, false); !res.Passed {
			t.Fatalf("mutation baseline must PASS: %+v", res)
		}
		if err := os.Remove(artifact); err != nil {
			t.Fatal(err)
		}
		if res := paRun(t, ctx, dir, wt, "Here is the plan.", nil, false); res.Passed {
			t.Fatalf("MUTATION NOT DETECTED: removing the plan artifact must flip to FAIL: %+v", res)
		}
	})
}
