package checkers

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// Gate T2 (07-bench/00T): pin the reply-quality llm_judge rubric set BEFORE
// implementation (07-bench/01). The rubrics are discovered from the hardened
// suites/usability-probe.json (the sanctioned delivery surface: llm_judge
// checks with rubric text), so this gate compiles against today's tree and
// fails RED until the rubric set lands. Contract under test:
//
//   - The usability suite carries reply-quality llm_judge rubrics covering
//     the audited defect classes: empty replies, raw envelope dumps,
//     "## review:" scaffolding chatter, job-ID prose, and clean contextual
//     replies (rubric text must name each class it grades).
//   - Rubrics score QUALITIES, never literal answers: every rubric passes
//     the answer-leak lint against every literal expected answer the suite
//     may pin in check extra.expected.
//   - The llmJudge path refuses (judge NEVER called) when a check's rubric
//     leaks its own extra.expected literal.
//   - Bad reply shapes score below the 0.7 default floor under a
//     rubric-faithful judge; a clean contextual reply passes.
//   - Double-judge >0.20 disagreement stays flagged with the new rubrics.
//
// RED reason until 07-bench/01 lands: no reply-quality llm_judge rubrics
// exist in suites/usability-probe.json (guard below).

// usabilityExpectedAnswers are the literal expected answers the hardened
// usability suite may pin in check extra.expected. Rubrics must stay blind
// to every one of them.
var usabilityExpectedAnswers = []string{
	"7",
	"ok",
	"The file definitely_missing_12345.txt does not exist.",
	"counter.txt now contains 7",
}

// loadUsabilityManifest loads the committed usability suite relative to
// this file (internal/checkers -> repo root is two levels up).
func loadUsabilityManifest(t *testing.T) *suite.Manifest {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := strings.TrimSuffix(thisFile, "/internal/checkers/reply_quality_test.go")
	m, err := suite.Load(repoRoot + "/suites/usability-probe.json")
	if err != nil {
		t.Fatalf("loading usability suite: %v", err)
	}
	return m
}

// replyQualityRubrics collects the reply-quality rubric texts from the
// usability suite's llm_judge checks. RED guard: none exist yet.
func replyQualityRubrics(t *testing.T) []string {
	t.Helper()
	m := loadUsabilityManifest(t)
	var rubrics []string
	for _, task := range m.Tasks {
		for _, c := range task.Checkers {
			if c.Type == "llm_judge" && strings.TrimSpace(c.Rubric) != "" {
				rubrics = append(rubrics, c.Rubric)
			}
		}
	}
	if len(rubrics) == 0 {
		t.Fatal("RED: reply-quality rubric set is not implemented (no llm_judge rubrics in suites/usability-probe.json)")
	}
	return rubrics
}

func TestReplyQualityJudgeLint(t *testing.T) {
	ctx := context.Background()
	rubrics := replyQualityRubrics(t)

	t.Run("rubric_set_covers_defect_classes", func(t *testing.T) {
		union := strings.ToLower(strings.Join(rubrics, "\n"))
		for _, needle := range []string{"empty", "envelope", "## review:", "job", "context"} {
			if !strings.Contains(union, needle) {
				t.Errorf("reply-quality rubric set must instruct the judge about %q", needle)
			}
		}
	})

	t.Run("rubrics_never_leak_expected_answers", func(t *testing.T) {
		for i, rubric := range rubrics {
			for _, expected := range usabilityExpectedAnswers {
				if err := lintRubric(rubric, expected); err != nil {
					t.Errorf("rubric[%d] leaks expected answer %q: %v", i, expected, err)
				}
			}
		}
	})

	t.Run("leaked_rubric_refuses_judge_call", func(t *testing.T) {
		calls := 0
		judge := judgeFunc(func(ctx context.Context, rubric, answer string) (float64, string, error) {
			calls++
			return 1.0, "should never run", nil
		})
		expected := "The file definitely_missing_12345.txt does not exist."
		c := suite.Check{
			Type:   "llm_judge",
			Rubric: rubrics[0] + " The correct reply is: " + expected,
			Extra:  json.RawMessage(fmt.Sprintf(`{"expected": %q}`, expected)),
		}
		res := Run(ctx, c, t.TempDir(), expected, judge)
		if res.Passed {
			t.Fatalf("rubric leaking the literal expected answer must FAIL: %+v", res)
		}
		if calls != 0 {
			t.Fatalf("judge was called %d times on a leaked rubric; it must NEVER see the answer", calls)
		}
		if !strings.Contains(res.Detail, "leaks the literal expected answer") {
			t.Fatalf("detail must carry the lint refusal, got %q", res.Detail)
		}
	})

	t.Run("clean_rubric_runs_judge", func(t *testing.T) {
		calls := 0
		judge := judgeFunc(func(ctx context.Context, rubric, answer string) (float64, string, error) {
			calls++
			return 0.9, "contextual and clean", nil
		})
		c := suite.Check{
			Type:   "llm_judge",
			Rubric: rubrics[0],
			Extra:  json.RawMessage(`{"expected": "The file definitely_missing_12345.txt does not exist."}`),
		}
		res := Run(ctx, c, t.TempDir(), "The file definitely_missing_12345.txt does not exist.", judge)
		if !res.Passed {
			t.Fatalf("clean rubric + good judge score must PASS: %+v", res)
		}
		if calls != 1 {
			t.Fatalf("judge calls = %d, want 1", calls)
		}
	})

	t.Run("bad_reply_shapes_score_fail", func(t *testing.T) {
		// A rubric-faithful deterministic judge: the reply shapes observed
		// in the usability audit must score below the 0.7 default floor
		// when graded against the reply-quality rubrics. This pins the
		// SCORING CONTRACT via a stub judge keyed on the defect shapes; the
		// live judge (scripts/judge-local.py) enforces the same rubrics.
		badReplies := []string{
			"",
			`{"conversation_id":"conv-1","status":"completed","reply":"done","duration_ms":1200}`,
			"## review:\nstep 1 approved\nstep 2 approved\n## end review",
			"Task task-9f3a2b completed. Job job-77 queued. See job-77 for details.",
		}
		judge := judgeFunc(func(ctx context.Context, rubric, answer string) (float64, string, error) {
			a := strings.TrimSpace(answer)
			switch {
			case a == "":
				return 0.0, "empty reply", nil
			case strings.HasPrefix(a, "{") && strings.Contains(a, `"status"`):
				return 0.1, "raw envelope dump", nil
			case strings.Contains(a, "## review:"):
				return 0.1, "review scaffolding chatter", nil
			case strings.Contains(a, "job-") && strings.Contains(a, "task-"):
				return 0.2, "job-ID prose, not user-shaped", nil
			default:
				return 0.9, "clean contextual reply", nil
			}
		})
		for i, reply := range badReplies {
			c := suite.Check{Type: "llm_judge", Rubric: rubrics[i%len(rubrics)]}
			res := Run(ctx, c, t.TempDir(), reply, judge)
			if res.Passed {
				t.Errorf("bad reply shape %d (%q) must FAIL the reply-quality rubric: %+v", i, reply, res)
			}
		}
		clean := suite.Check{Type: "llm_judge", Rubric: rubrics[0]}
		res := Run(ctx, clean, t.TempDir(),
			"I could not read definitely_missing_12345.txt because it does not exist in the repository root.", judge)
		if !res.Passed {
			t.Errorf("clean contextual reply must PASS: %+v", res)
		}
	})

	t.Run("double_judge_disagreement_flagged_with_rubric", func(t *testing.T) {
		c := suite.Check{Type: "llm_judge", Rubric: rubrics[0]}
		res := Run(ctx, c, t.TempDir(), "some reply", stubJudge(0.9, 0.5, nil), WithDoubleJudge())
		if !strings.Contains(res.Detail, "DISAGREEMENT") {
			t.Fatalf("double-judge disagreement must be flagged: %+v", res)
		}
	})
}

func TestNoAnswerLeak(t *testing.T) {
	ctx := context.Background()
	rubrics := replyQualityRubrics(t)

	// Sweep: for EVERY suite rubric x EVERY usability expected answer, a
	// check carrying that expected literal must never let the judge run
	// when the rubric contains the literal, and must run when it does not.
	for i, rubric := range rubrics {
		for _, expected := range usabilityExpectedAnswers {
			if len([]rune(strings.TrimSpace(expected))) < minRubricLintLen {
				// Tiny literals ("7", "ok") are lint-exempt by design.
				continue
			}

			t.Run(fmt.Sprintf("rubric%d/clean", i), func(t *testing.T) {
				calls := 0
				judge := judgeFunc(func(ctx context.Context, rubric, answer string) (float64, string, error) {
					calls++
					return 0.9, "ok", nil
				})
				c := suite.Check{
					Type:   "llm_judge",
					Rubric: rubric,
					Extra:  json.RawMessage(fmt.Sprintf(`{"expected": %q}`, expected)),
				}
				res := Run(ctx, c, t.TempDir(), expected, judge)
				if calls != 1 || !res.Passed {
					t.Fatalf("clean rubric[%d] with expected %q: judge calls=%d res=%+v", i, expected, calls, res)
				}
			})

			t.Run(fmt.Sprintf("rubric%d/leaked", i), func(t *testing.T) {
				calls := 0
				judge := judgeFunc(func(ctx context.Context, rubric, answer string) (float64, string, error) {
					calls++
					return 1.0, "must not run", nil
				})
				c := suite.Check{
					Type:   "llm_judge",
					Rubric: rubric + "\nExpected answer: " + expected,
					Extra:  json.RawMessage(fmt.Sprintf(`{"expected": %q}`, expected)),
				}
				res := Run(ctx, c, t.TempDir(), expected, judge)
				if res.Passed || calls != 0 {
					t.Fatalf("leaked rubric[%d] with expected %q must refuse the judge: calls=%d res=%+v", i, expected, calls, res)
				}
			})
		}
	}
}
