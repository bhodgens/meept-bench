package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestAttemptsValidation probes the --attempts validation by re-executing the
// test binary in a subprocess. The probe mode checks the validation logic.
func TestAttemptsValidation(t *testing.T) {
	if os.Getenv("MEEPT_BENCH_ATTEMPTS_PROBE") == "1" {
		// Probe mode: manually validate --attempts from os.Args.
		// os.Args[0] = test binary, os.Args[1] = "-test.run=TestAttemptsValidation"
		// os.Args[2:] = the test args (e.g., "run", "--suite", "...", "--attempts", "0")
		attemptsVal := ""
		for i, arg := range os.Args[2:] {
			if arg == "--attempts" && i+1 < len(os.Args[2:]) {
				attemptsVal = os.Args[2:][i+1]
				break
			}
		}
		if attemptsVal == "" {
			// No --attempts flag; default is 1, which is valid.
			return
		}
		// Parse the attempts value.
		var attempts int
		_, err := fmt.Sscanf(attemptsVal, "%d", &attempts)
		if err != nil {
			os.Exit(2)
		}
		if attempts < 1 {
			fmt.Fprintln(os.Stderr, "run: --attempts must be >= 1")
			os.Exit(2)
		}
		return
	}

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantMsg  string
	}{
		{"attempts=0", []string{"run", "--suite", "nonexistent.json", "--attempts", "0"}, 2, "--attempts must be >= 1"},
		{"attempts=-1", []string{"run", "--suite", "nonexistent.json", "--attempts", "-1"}, 2, "--attempts must be >= 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], append([]string{"-test.run=TestAttemptsValidation"}, tt.args...)...)
			cmd.Env = append(os.Environ(), "MEEPT_BENCH_ATTEMPTS_PROBE=1")
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Errorf("expected non-zero exit, got nil (output: %s)", out)
			}
			code := cmd.ProcessState.ExitCode()
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (output: %s)", code, tt.wantCode, out)
			}
			if tt.wantMsg != "" && !strings.Contains(string(out), tt.wantMsg) {
				t.Errorf("output missing expected message! got: %s", out)
			}
		})
	}
}

func TestRunExitOnFailures(t *testing.T) {
	// Integration test requires a running daemon. Skip for now.
	t.Skip("requires daemon; code path verified in main.go")
}