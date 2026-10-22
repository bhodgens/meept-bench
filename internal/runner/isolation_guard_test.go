package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// TestRunSuiteRefusesUserStateDir pins the defense-in-depth isolation guard:
// RunSuite must fail before dispatching ANY task when the effective socket
// (MEEPT_BENCH_SOCKET, else $HOME/.meept/meept.sock) resolves inside the
// user's live state dir — even when the CLI-level guard was bypassed by
// calling runner.New directly (BENCH-7391 contamination, 2026-10-22).
func TestRunSuiteRefusesUserStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MEEPT_BENCH_SOCKET", "") // fall back to $HOME/.meept/meept.sock

	repo := initBenchRepo(t)
	r, err := New(Options{RepoPath: repo, ScratchRoot: t.TempDir(), OutDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m := &suite.Manifest{Suite: "isolation-guard", Tasks: []suite.Task{{ID: "never-runs", Prompt: "hi"}}}

	outDir, rows, err := r.RunSuite(context.Background(), m, "")
	if err == nil {
		t.Fatalf("RunSuite against the user's live state dir succeeded (outDir=%q, rows=%d); want refusal", outDir, len(rows))
	}
	if !strings.Contains(err.Error(), "isolation guard") {
		t.Errorf("error does not name the isolation guard: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("guard fired but %d rows were produced", len(rows))
	}
}

// TestRunSuiteAllowsScratchSocket is the positive control: a socket outside
// the user's state dir passes the guard (the run then fails later on daemon
// unreachability — that is fine, the guard is what is under test).
func TestRunSuiteAllowsScratchSocket(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MEEPT_BENCH_SOCKET", filepath.Join(t.TempDir(), "rig", "meept.sock"))

	repo := initBenchRepo(t)
	r, err := New(Options{RepoPath: repo, ScratchRoot: t.TempDir(), OutDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m := &suite.Manifest{Suite: "isolation-guard", Tasks: []suite.Task{{ID: "dispatch-attempt", Prompt: "hi"}}}

	_, _, err = r.RunSuite(context.Background(), m, "")
	if err != nil && strings.Contains(err.Error(), "isolation guard") {
		t.Errorf("scratch-rig socket wrongly refused: %v", err)
	}
}

// initBenchRepo creates a minimal git repo the isolate.Manager can fork
// worktrees from.
func initBenchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "bench@test")
	run("config", "user.name", "bench")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("bench repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	return dir
}
