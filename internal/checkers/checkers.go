// Package checkers implements task verdict checkers.
package checkers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// Judge is the llm_judge backend. Judges run at temperature 0 and are blind
// to model identity (only the answer text is passed).
type Judge interface {
	Judge(ctx context.Context, rubric, answer string) (score float64, rationale string, err error)
}

// Result is one checker outcome.
type Result struct {
	Check     string  `json:"check"`
	Passed    bool    `json:"passed"`
	Score     float64 `json:"score,omitempty"`
	Detail    string  `json:"detail,omitempty"`
	Rationale string  `json:"rationale,omitempty"`
}

// RunOption customizes a Run call.
type RunOption func(*runConfig)

// runConfig holds per-Run options.
type runConfig struct {
	doubleJudge bool
}

// WithDoubleJudge runs the llm_judge backend twice and reports the mean
// score, flagging disagreement beyond doubleJudgeTolerance in the Result
// detail. Can also be enabled process-wide via MEEPT_BENCH_DOUBLE_JUDGE.
func WithDoubleJudge() RunOption {
	return func(rc *runConfig) { rc.doubleJudge = true }
}

// Run executes a checker against the task worktree.
func Run(ctx context.Context, c suite.Check, worktree, finalAnswer string, judge Judge, opts ...RunOption) Result {
	var rc runConfig
	for _, o := range opts {
		o(&rc)
	}
	var r Result
	r.Check = c.Type
	var err error
	switch c.Type {
	case "exact_file":
		r.Passed, r.Detail, err = exactFile(c, worktree)
	case "file_contains":
		r.Passed, r.Detail, err = fileContains(c, worktree)
	case "exit_zero":
		r.Passed, r.Detail, err = exitZero(ctx, c, worktree)
	case "llm_judge":
		r.Score, r.Rationale, r.Detail, err = llmJudge(ctx, c, finalAnswer, judge, rc.doubleJudge)
		r.Passed = err == nil && r.Score >= minScore(c.MinScore)
	default:
		return Result{Check: c.Type, Passed: false, Detail: "unknown checker type"}
	}
	if err != nil && r.Detail == "" {
		r.Detail = err.Error()
	}
	return r
}

func minScore(v float64) float64 {
	if v == 0 {
		return 0.7
	}
	return v
}

func exactFile(c suite.Check, wt string) (bool, string, error) {
	path := join(wt, c.File)
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Sprintf("file missing/unreadable: %v", err), nil
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != c.Hash {
		return false, fmt.Sprintf("sha256 mismatch: got %s want %s", short(got), short(c.Hash)), nil
	}
	return true, "content hash matches", nil
}

func fileContains(c suite.Check, wt string) (bool, string, error) {
	re, err := regexp.Compile(c.Pattern)
	if err != nil {
		return false, "", fmt.Errorf("bad pattern: %w", err)
	}
	// Suite patterns conventionally use ^/$ anchors, but Go RE2 binds those to
	// the whole TEXT, not per line. Files end with a trailing newline, so
	// "^42$" can never match "42\n". Trim ONE trailing newline before
	// matching; embedded newlines still make ^/$ unmatchable mid-file.
	files := c.Files
	if len(files) == 0 && c.File != "" {
		files = []string{c.File}
	}
	for _, f := range files {
		data, err := os.ReadFile(join(wt, f))
		if err != nil {
			continue
		}
		data = trimTrailingNewline(data)
		if re.Match(data) {
			return true, fmt.Sprintf("pattern found in %s", f), nil
		}
	}
	return false, fmt.Sprintf("pattern not found in any of %d file(s)", len(files)), nil
}

func trimTrailingNewline(data []byte) []byte {
	trimmed := bytes.TrimRight(data, "\n")
	// Trim at most one newline's worth: "42\n\n" stays "42\n" so a deliberately
	// blank final line still fails ^42$. TrimRight above removes all of them;
	// restore one if two or more were removed.
	if len(data)-len(trimmed) > 1 {
		return trimmed[:len(trimmed)+1]
	}
	return trimmed
}

func exitZero(ctx context.Context, c suite.Check, wt string) (bool, string, error) {
	if len(c.Command) == 0 {
		return false, "", fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, c.Command[0], c.Command[1:]...)
	cmd.Dir = wt
	out, err := cmd.CombinedOutput()
	detail := trim(out)
	if err != nil {
		return false, detail, nil
	}
	return true, detail, nil
}

// doubleJudgeEnvEnv toggles double-judging process-wide (e.g.
// MEEPT_BENCH_DOUBLE_JUDGE=1), complementing the per-call WithDoubleJudge.
const doubleJudgeEnvEnv = "MEEPT_BENCH_DOUBLE_JUDGE"

// doubleJudgeTolerance is the max |score1 - score2| treated as judge
// agreement; beyond it the run is flagged as unreliable in the Result detail.
const doubleJudgeTolerance = 0.2

// llmJudge scores the final answer via the judge backend. With double-judge
// enabled (WithDoubleJudge or MEEPT_BENCH_DOUBLE_JUDGE) it judges twice,
// reports the mean score, and flags disagreement beyond
// doubleJudgeTolerance in the detail so unreliable verdicts are visible.
func llmJudge(ctx context.Context, c suite.Check, answer string, judge Judge, double bool) (float64, string, string, error) {
	if judge == nil {
		return 0, "", "", fmt.Errorf("no judge configured (set MEEPT_BENCH_JUDGE_CMD or --judge-cmd)")
	}
	// Rubric lint: refuse to run a judge that has already seen the answer
	// it is supposed to be blind to.
	if err := lintRubric(c.Rubric, checkExpected(c.Extra)); err != nil {
		return 0, "", "", err
	}

	if !(double || envFlag(doubleJudgeEnvEnv)) {
		score, why, err := judge.Judge(ctx, c.Rubric, answer)
		return score, why, "", err
	}

	s1, why1, err1 := judge.Judge(ctx, c.Rubric, answer)
	s2, why2, err2 := judge.Judge(ctx, c.Rubric, answer)
	if err1 != nil || err2 != nil {
		// Prefer surfacing the first error; keep the second as context.
		if err1 == nil {
			err1 = err2
		}
		return 0, "", fmt.Sprintf("judge attempt 2: %v", err2), err1
	}
	mean := math.Round((s1+s2)/2*10000) / 10000
	detail := fmt.Sprintf("double-judge: scores %.2f/%.2f (mean %.2f)", s1, s2, mean)
	if math.Abs(s1-s2) > doubleJudgeTolerance {
		detail += fmt.Sprintf("; DISAGREEMENT >%.2f — verdict unreliable, inspect rationale", doubleJudgeTolerance)
	}
	// Keep the first rationale, note the second when it differs.
	why := why1
	if why2 != "" && why2 != why1 {
		why = why1 + " | second judge: " + why2
	}
	return mean, why, detail, nil
}

// envFlag reads a boolean env var ("1", "true", "yes", "on", case-insensitive).
func envFlag(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// checkExpected extracts the optional literal expected answer from the
// check's `extra` field ({"expected": "..."}), used only for rubric linting.
// All decode failures degrade to "no expected" — never an error, so a
// malformed extra field can't break an otherwise valid suite.
func checkExpected(extra json.RawMessage) string {
	if len(extra) == 0 {
		return ""
	}
	var m struct {
		Expected string `json:"expected"`
	}
	if err := json.Unmarshal(extra, &m); err != nil {
		return ""
	}
	return m.Expected
}

func join(wt, p string) string {
	if isAbs(p) {
		return p
	}
	return wt + "/" + p
}

func isAbs(p string) bool { return len(p) > 0 && p[0] == '/' }

func short(h string) string {
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}

func trim(b []byte) string {
	const max = 2000
	s := string(b)
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
