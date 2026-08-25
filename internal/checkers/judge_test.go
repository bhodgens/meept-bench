package checkers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestCmdJudgeWithScript uses a real judge script that prints "0.9 good".
func TestCmdJudgeWithScript(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "judge.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nhead -c 1 /dev/stdin >/dev/null; echo '0.9 rationale here'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	j, err := NewCmdJudge(script)
	if err != nil {
		t.Fatal(err)
	}
	score, why, err := j.Judge(context.Background(), "rubric", "answer")
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if score != 0.9 || why != "rationale here" {
		t.Fatalf("got score=%v why=%q", score, why)
	}
}
