// Package checkers implements task verdict checkers.
package checkers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"regexp"

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

// Run executes a checker against the task worktree.
func Run(ctx context.Context, c suite.Check, worktree, finalAnswer string, judge Judge) Result {
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
		r.Score, r.Rationale, err = llmJudge(ctx, c, finalAnswer, judge)
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

func llmJudge(ctx context.Context, c suite.Check, answer string, judge Judge) (float64, string, error) {
	if judge == nil {
		return 0, "", fmt.Errorf("no judge configured (set MEEPT_BENCH_JUDGE_CMD or --judge-cmd)")
	}
	score, why, err := judge.Judge(ctx, c.Rubric, answer)
	return score, why, err
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
