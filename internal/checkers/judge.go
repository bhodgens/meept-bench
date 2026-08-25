package checkers

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// CmdJudge runs an external judge command. The command receives
// "<rubric>\n---\n<answer>" on stdin and must print a single line:
//
//	<score 0..1> <rationale...>
type CmdJudge struct {
	Cmd []string
}

// NewCmdJudge parses a judge command spec ("prog arg1 arg2").
func NewCmdJudge(spec string) (*CmdJudge, error) {
	parts := strings.Fields(spec)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty judge command")
	}
	return &CmdJudge{Cmd: parts}, nil
}

// Judge implements Judge at temperature 0 semantics (deterministic external call).
func (j *CmdJudge) Judge(ctx context.Context, rubric, answer string) (float64, string, error) {
	cmd := exec.CommandContext(ctx, j.Cmd[0], j.Cmd[1:]...)
	cmd.Stdin = strings.NewReader(rubric + "\n---\n" + answer + "\n")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return 0, out.String(), fmt.Errorf("judge failed: %w", err)
	}
	line := strings.TrimSpace(out.String())
	fields := strings.SplitN(line, " ", 2)
	score, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, line, fmt.Errorf("judge output does not start with a score: %q", line)
	}
	why := ""
	if len(fields) == 2 {
		why = fields[1]
	}
	return clamp01(score), why, nil
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}
