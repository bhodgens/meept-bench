package checkers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// TestLintRubricRejectsExpectedAnswer: a rubric that contains the literal
// expected answer (case-insensitively) must be rejected.
func TestLintRubricRejectsExpectedAnswer(t *testing.T) {
	err := lintRubric("the file must contain the token XYZ123", "xyz123")
	if err == nil || !strings.Contains(err.Error(), "leaks the literal expected answer") {
		t.Fatalf("want leak rejection, got %v", err)
	}
}

// TestLintRubricPassesCleanRubric: a rubric describing qualities (not the
// answer) passes; empty expected disables the lint.
func TestLintRubricPassesCleanRubric(t *testing.T) {
	for _, tc := range []struct{ rubric, expected string }{
		{"does the reply explain recursion?", "abacus"},
		{"any rubric", ""},
		{"", "anything"},
	} {
		if err := lintRubric(tc.rubric, tc.expected); err != nil {
			t.Fatalf("lintRubric(%q, %q): %v", tc.rubric, tc.expected, err)
		}
	}
}

// TestLintRubricShortExpectedSkipped: tiny expected strings would
// substring-match almost any rubric; the lint skips them.
func TestLintRubricShortExpectedSkipped(t *testing.T) {
	if err := lintRubric("score the reply structure", "a"); err != nil {
		t.Fatalf("single-char expected must be skipped, got %v", err)
	}
}

// TestLintFailsLLMJudge end-to-end: a check whose rubric carries the
// expected answer in `extra.expected` must fail the checker with a clear
// detail — and must never call the judge.
func TestLintFailsLLMJudge(t *testing.T) {
	calls := 0
	judge := judgeFunc(func(ctx context.Context, rubric, answer string) (float64, string, error) {
		calls++
		return 1.0, "called", nil
	})
	extra, _ := json.Marshal(map[string]string{"expected": "hunter2"})
	r := Run(context.Background(), suite.Check{
		Type: "llm_judge", Rubric: "reply must include hunter2", Extra: extra,
	}, wt(t), "hunter2", judge)
	if r.Passed {
		t.Fatalf("lint failure must fail the check, got %+v", r)
	}
	if calls != 0 {
		t.Fatalf("judge must not be called on lint failure, calls=%d", calls)
	}
	if !strings.Contains(r.Detail, "leaks the literal expected answer") {
		t.Fatalf("detail must carry lint reason, got %q", r.Detail)
	}
}

// judgeFunc adapts a function to the Judge interface.
type judgeFunc func(ctx context.Context, rubric, answer string) (float64, string, error)

func (f judgeFunc) Judge(ctx context.Context, rubric, answer string) (float64, string, error) {
	return f(ctx, rubric, answer)
}

// stubJudge returns a Judge emitting fixed per-call scores (alternating
// s1, s2, s1, …) with an optional error on the second call.
func stubJudge(s1, s2 float64, secondErr error) judgeFunc {
	call := 0
	return judgeFunc(func(ctx context.Context, rubric, answer string) (float64, string, error) {
		call++
		if call == 2 && secondErr != nil {
			return 0, "", secondErr
		}
		if call%2 == 1 {
			return s1, "stub rationale", nil
		}
		return s2, "stub rationale 2", nil
	})
}

// TestDoubleJudgeMeanAndDetail: two nearly-agreeing judges -> mean score,
// detail notes both scores, no disagreement flag.
func TestDoubleJudgeMeanAndDetail(t *testing.T) {
	r := Run(context.Background(), suite.Check{Type: "llm_judge", Rubric: "quality?"},
		wt(t), "answer", stubJudge(0.8, 0.9, nil), WithDoubleJudge())
	if r.Score != 0.85 {
		t.Fatalf("score = %v, want mean 0.85", r.Score)
	}
	if !strings.Contains(r.Detail, "double-judge: scores 0.80/0.90") || !strings.Contains(r.Detail, "mean 0.85") {
		t.Fatalf("detail = %q, want double-judge scores+mean", r.Detail)
	}
	if strings.Contains(r.Detail, "DISAGREEMENT") {
		t.Fatalf("0.1 delta must not flag disagreement, detail = %q", r.Detail)
	}
}

// TestDoubleJudgeDisagreementFlag: judges 0.5 apart -> mean kept, detail
// flags the verdict as unreliable. Uses a REAL CmdJudge script stub so the
// --judge-cmd path is exercised end to end.
func TestDoubleJudgeDisagreementFlag(t *testing.T) {
	j, err := NewCmdJudge(alternatingJudgeScript(t))
	if err != nil {
		t.Fatal(err)
	}
	r := Run(context.Background(), suite.Check{Type: "llm_judge", Rubric: "quality?"},
		wt(t), "answer", j, WithDoubleJudge())
	if r.Score != 0.65 {
		t.Fatalf("score = %v, want mean 0.65", r.Score)
	}
	if !strings.Contains(r.Detail, "DISAGREEMENT") {
		t.Fatalf("0.5 delta must flag disagreement, detail = %q", r.Detail)
	}
	if !strings.Contains(r.Rationale, "second judge:") {
		t.Fatalf("rationale must carry the second judge's view, got %q", r.Rationale)
	}
}

// TestDoubleJudgeEnvToggle: MEEPT_BENCH_DOUBLE_JUDGE=1 enables double
// judging without the RunOption.
func TestDoubleJudgeEnvToggle(t *testing.T) {
	t.Setenv("MEEPT_BENCH_DOUBLE_JUDGE", "1")
	r := Run(context.Background(), suite.Check{Type: "llm_judge", Rubric: "quality?"},
		wt(t), "answer", stubJudge(1.0, 0.5, nil))
	if r.Score != 0.75 {
		t.Fatalf("env toggle must enable double-judge, score = %v", r.Score)
	}
}

// TestSingleJudgeNoDetail: without double-judge, behavior is unchanged —
// first score only, no extra detail.
func TestSingleJudgeNoDetail(t *testing.T) {
	r := Run(context.Background(), suite.Check{Type: "llm_judge", Rubric: "quality?"},
		wt(t), "answer", stubJudge(0.9, 0.1, nil))
	if r.Score != 0.9 {
		t.Fatalf("single-judge mode must use first score only, got %v", r.Score)
	}
	if r.Detail != "" {
		t.Fatalf("single-judge mode must not add detail, got %q", r.Detail)
	}
}

// TestDoubleJudgeSecondCallFails: when either judge attempt errors, the
// checker errors and the detail carries the second attempt's context.
func TestDoubleJudgeSecondCallFails(t *testing.T) {
	r := Run(context.Background(), suite.Check{Type: "llm_judge", Rubric: "quality?"},
		wt(t), "answer", stubJudge(0.9, 0, errors.New("boom")), WithDoubleJudge())
	if r.Passed {
		t.Fatal("judge error must fail the check")
	}
	if !strings.Contains(r.Detail, "judge attempt 2") || !strings.Contains(r.Detail, "boom") {
		t.Fatalf("detail must carry second-attempt error, got %q", r.Detail)
	}
}

// alternatingJudgeScript writes a judge stub that alternates its score per
// invocation (0.4, 0.9, 0.4, …) via a counter file in the test's temp dir.
func alternatingJudgeScript(t *testing.T) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "judge-alternating.sh")
	countFile := filepath.Join(t.TempDir(), "count")
	body := "#!/bin/sh\ncat >/dev/null\n" +
		"n=0; [ -f \"" + countFile + "\" ] && n=$(cat \"" + countFile + "\")\n" +
		"n=$((n+1)); echo $n >\"" + countFile + "\"\n" +
		"if [ $((n%2)) -eq 1 ]; then echo '0.4 first judge'; else echo '0.9 second judge'; fi\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}
