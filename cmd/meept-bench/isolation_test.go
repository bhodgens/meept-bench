package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func setHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
}

func TestIsUserDaemonSocket(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	tests := []struct {
		path string
		want bool
	}{
		{filepath.Join(home, ".meept", "meept.sock"), true},
		{filepath.Join(home, ".meept", "other"), true}, // under the dir
		{filepath.Join(home, "rig", "home", ".meept", "meept.sock"), false},
		{filepath.Join(t.TempDir(), ".meept", "meept.sock"), false},
		{"/tmp/meept/meept.sock", false},
	}
	for _, tt := range tests {
		if got := isUserDaemonSocket(tt.path); got != tt.want {
			t.Errorf("isUserDaemonSocket(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestDefaultSocketPathEnvOverride(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	t.Setenv("MEEPT_BENCH_SOCKET", "/tmp/rig/.meept/meept.sock")
	if got := defaultSocketPath(); got != "/tmp/rig/.meept/meept.sock" {
		t.Errorf("defaultSocketPath() with env = %q, want env value", got)
	}

	t.Setenv("MEEPT_BENCH_SOCKET", "")
	want := filepath.Join(home, ".meept", "meept.sock")
	if got := defaultSocketPath(); got != want {
		t.Errorf("defaultSocketPath() = %q, want %q", got, want)
	}
}

func TestRequireIsolatedSocketAllowsScratch(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("MEEPT_BENCH_SOCKET", "/tmp/rig/home/.meept/meept.sock")
	requireIsolatedSocket() // must not exit
}

// TestRequireIsolatedSocketBlocksUserSocket re-executes the test binary in a
// subprocess so the guard's os.Exit(2) doesn't kill the test process.
func TestRequireIsolatedSocketBlocksUserSocket(t *testing.T) {
	if os.Getenv("MEEPT_BENCH_GUARD_PROBE") == "1" {
		home := os.Getenv("MEEPT_BENCH_GUARD_HOME")
		t.Setenv("HOME", home)
		os.Unsetenv("MEEPT_BENCH_SOCKET")
		requireIsolatedSocket() // expected to exit(2) here
		return
	}
	home := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestRequireIsolatedSocketBlocksUserSocket")
	cmd.Env = append(os.Environ(),
		"MEEPT_BENCH_GUARD_PROBE=1",
		"MEEPT_BENCH_GUARD_HOME="+home,
	)
	out, err := cmd.CombinedOutput()
	if err != nil && cmd.ProcessState.ExitCode() != 2 {
		t.Fatalf("subprocess error: %v (output: %s)", err, out)
	}
	if code := cmd.ProcessState.ExitCode(); code != 2 {
		t.Errorf("guard exit code = %d, want 2 (output: %s)", code, out)
	}
	if !strings.Contains(string(out), "refusing to talk to the user's live meept daemon") {
		t.Errorf("guard message missing from output: %s", out)
	}
}
