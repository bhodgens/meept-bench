package checkers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bhodgens/meept-bench/internal/suite"
)

func wt(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "answer.txt"), []byte("the answer is 42"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestExactFilePassFail(t *testing.T) {
	data, _ := os.ReadFile(filepath.Join(wt(t), "answer.txt"))
	sum := hex.EncodeToString(func() []byte { s := sha256.Sum256(data); return s[:] }())
	r := Run(context.Background(), suite.Check{Type: "exact_file", File: "answer.txt", Hash: sum}, wt(t), "", nil)
	if !r.Passed {
		t.Fatalf("want pass, got %+v", r)
	}
	r = Run(context.Background(), suite.Check{Type: "exact_file", File: "answer.txt", Hash: "deadbeef"}, wt(t), "", nil)
	if r.Passed || r.Detail == "" {
		t.Fatalf("want fail with detail, got %+v", r)
	}
}

func TestFileContains(t *testing.T) {
	r := Run(context.Background(), suite.Check{Type: "file_contains", Files: []string{"answer.txt"}, Pattern: "answer is \\d+"}, wt(t), "", nil)
	if !r.Passed {
		t.Fatalf("want pass, got %+v", r)
	}
	r = Run(context.Background(), suite.Check{Type: "file_contains", Files: []string{"answer.txt"}, Pattern: "answer is 43"}, wt(t), "", nil)
	if r.Passed {
		t.Fatalf("want fail, got %+v", r)
	}
}

// TestFileContainsMultipleFiles: the checker passes if ANY listed file
// matches (first-match-wins), and skips files that don't exist.
func TestFileContainsMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// b.txt intentionally missing — a matching existing file must still pass.
	r := Run(context.Background(), suite.Check{Type: "file_contains", Files: []string{"missing.txt", "a.txt"}, Pattern: "alpha"}, dir, "", nil)
	if !r.Passed {
		t.Fatalf("want pass via second file, got %+v", r)
	}
	// No file matches.
	r = Run(context.Background(), suite.Check{Type: "file_contains", Files: []string{"missing.txt", "a.txt"}, Pattern: "omega"}, dir, "", nil)
	if r.Passed {
		t.Fatalf("want fail, got %+v", r)
	}
	// Single legacy "file" field form still works.
	r = Run(context.Background(), suite.Check{Type: "file_contains", File: "a.txt", Pattern: "alpha"}, dir, "", nil)
	if !r.Passed {
		t.Fatalf("want pass via legacy file field, got %+v", r)
	}
}

func TestExitZero(t *testing.T) {
	r := Run(context.Background(), suite.Check{Type: "exit_zero", Command: []string{"cat", "answer.txt"}}, wt(t), "", nil)
	if !r.Passed {
		t.Fatalf("want pass, got %+v", r)
	}
	r = Run(context.Background(), suite.Check{Type: "exit_zero", Command: []string{"false"}}, wt(t), "", nil)
	if r.Passed {
		t.Fatalf("want fail, got %+v", r)
	}
}

func TestLLMJudgeRequiresJudge(t *testing.T) {
	r := Run(context.Background(), suite.Check{Type: "llm_judge", Rubric: "is it correct?"}, wt(t), "42", nil)
	if r.Passed {
		t.Fatal("llm_judge without judge must fail")
	}
}

func TestCmdJudgeEcho(t *testing.T) {
	j, err := NewCmdJudge("cat")
	if err != nil {
		t.Fatal(err)
	}
	// cat echoes the input; the score parse must fail with a clear message.
	_, _, err = j.Judge(context.Background(), "rubric", "answer")
	if err == nil || !strings.Contains(err.Error(), "does not start with a score") {
		t.Fatalf("want score-parse error, got %v", err)
	}
}
