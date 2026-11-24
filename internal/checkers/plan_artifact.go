package checkers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// plan_artifact is the outcome-level plan checker replacing the vacuous
// exit_zero(true) on u-plan-mode-request (plan 20261119-usability-remediation,
// I8). PASS requires REAL plan evidence -- any one of:
//
//  (a) a non-empty plan artifact in the task worktree: plan.md or PLAN.md at
//      the root, or a non-empty *.md under plans/;
//  (b) plan-draft lifecycle evidence in the current-run transcript's
//      tool_trace: a plan.created / plan.draft / plan.sealed / plan.submitting
//      bus event;
//  (c) a judge-verified plan reply: the check carries a rubric and the judge
//      scores the final reply >= the min_score floor.
//
// Reply prose alone (plan-shaped text, step-log dumps) never passes without
// one of the evidence paths above -- no assumed chain credit. Empty plan
// files do not count.
//
// Transcript reads follow the same current-run discipline as
// terminal_delivery: WithTranscriptDir option, else MEEPT_BENCH_TRANSCRIPT_DIR
// env; never a results/** glob. Missing extra.task_id fails; a transcript
// whose task_id differs fails (no association, no credit). With no
// transcript dir at all the checker fails closed.

// planDraftTopics are the bus topics accepted as plan-draft lifecycle evidence.
var planDraftTopics = map[string]bool{
	"plan.created":    true,
	"plan.draft":      true,
	"plan.sealed":     true,
	"plan.submitting": true,
}

// runPlanArtifact implements the plan_artifact checker. worktree is the
// task worktree handed to Run; finalAnswer is the transcript's final reply.
func runPlanArtifact(ctx context.Context, c suite.Check, worktree string, rc runConfig, finalAnswer string, judge Judge) (bool, string, error) {
	taskID := extraString(c.Extra, "task_id")
	if taskID == "" {
		return false, "", fmt.Errorf("plan_artifact: check extra.task_id is required (no association, no credit)")
	}
	dir, err := transcriptDirFor(rc)
	if err != nil {
		return false, "", err
	}
	// Association gate: the current-run transcript must exist and carry the
	// task's own id, or the checker fails closed.
	if _, err := loadTaskTranscript(dir, taskID); err != nil {
		return false, "", err
	}

	var evidence []string

	// (a) Worktree plan artifact (non-empty plan.md / PLAN.md / plans/*.md).
	if detail, ok := findPlanArtifact(worktree); ok {
		evidence = append(evidence, detail)
	}

	// (b) Plan-draft lifecycle events in the current-run transcript.
	if topics := findPlanDraftTopics(dir, taskID); len(topics) > 0 {
		evidence = append(evidence, "plan-draft lifecycle event(s): "+strings.Join(topics, ", "))
	}

	// (c) Judge-verified plan reply -- only consulted when no artifact or
	// draft evidence exists, so the judge is not burned pointlessly.
	if len(evidence) == 0 && strings.TrimSpace(c.Rubric) != "" && judge != nil {
		score, _, _, err := llmJudge(ctx, c, finalAnswer, judge, false)
		if err != nil {
			return false, "", err
		}
		if score >= minScore(c.MinScore) {
			evidence = append(evidence, fmt.Sprintf("judge-verified plan reply (score %.2f >= floor)", score))
		}
	}

	if len(evidence) == 0 {
		return false, "no plan evidence: no non-empty plan.md/PLAN.md/plans/*.md artifact, no plan-draft lifecycle event in the current-run transcript, and no judge-verified plan reply (reply prose alone never satisfies this checker)", nil
	}
	return true, "plan_artifact: " + strings.Join(evidence, "; "), nil
}

// findPlanArtifact looks for a non-empty plan artifact in the worktree:
// plan.md or PLAN.md at the root, or any non-empty *.md directly under
// plans/. Returns the evidence detail and whether one was found.
func findPlanArtifact(worktree string) (string, bool) {
	candidates := []string{"plan.md", "PLAN.md"}
	if entries, err := os.ReadDir(filepath.Join(worktree, "plans")); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
				candidates = append(candidates, filepath.Join("plans", e.Name()))
			}
		}
	}
	sort.Strings(candidates)
	for _, rel := range candidates {
		data, err := os.ReadFile(filepath.Join(worktree, rel))
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			continue // missing or empty plans do not count
		}
		return fmt.Sprintf("non-empty plan artifact %s", rel), true
	}
	return "", false
}

// findPlanDraftTopics returns the plan-draft lifecycle topics present in the
// task's current-run transcript tool_trace (sorted, deduplicated).
func findPlanDraftTopics(dir, taskID string) []string {
	tr, err := loadTaskTranscript(dir, taskID)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var topics []string
	for _, ev := range tr.ToolTrace {
		if planDraftTopics[ev.Topic] && !seen[ev.Topic] {
			seen[ev.Topic] = true
			topics = append(topics, ev.Topic)
		}
	}
	sort.Strings(topics)
	return topics
}
