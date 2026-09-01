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

// TestCmdJudgeSuccess runs a scored judge command via "sh -c" and checks
// the score and rationale returned by Judge.
func TestCmdJudgeSuccess(t *testing.T) {
	// CmdJudge holds a tokenized argv, so build it directly: the shell
	// snippet must stay a single argument to -c (NewCmdJudge's
	// strings.Fields would split it apart).
	j := &CmdJudge{Cmd: []string{"sh", "-c", "cat >/dev/null; echo '0.85 rationale here'"}}
	score, why, err := j.Judge(context.Background(), "rubric", "answer")
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if score != 0.85 {
		t.Fatalf("score = %v, want 0.85", score)
	}
	if why != "rationale here" {
		t.Fatalf("why = %q, want %q", why, "rationale here")
	}
}

// TestClamp01 checks clamping to the [0,1] range at and beyond the boundaries.
func TestClamp01(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{-1, 0},
		{0, 0},
		{0.5, 0.5},
		{1, 1},
		{2, 1},
	}
	for _, c := range cases {
		if got := clamp01(c.in); got != c.want {
			t.Fatalf("clamp01(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
