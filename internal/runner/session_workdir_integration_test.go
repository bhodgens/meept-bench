package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// execGit runs a git command in dir, for cleaning up kept worktrees.
func execGit(t *testing.T, dir string, args ...string) error {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

// TestSessionWorkdirTaskAcceptsBoundWorktree drives the REAL
// session-workdir-bound task from suites/regression.json through RunTask with
// a fake daemon: the agent writes cwd.txt containing the worktree the runner
// bound (what meept's shell tool now resolves to), and the row must PASS.
//
// This is the regression test for the bug where the checker's pattern only
// matched `.meept-bench/` and `/tmp/meept-bench/`, so the correct scratch-rig
// worktree path — /private/tmp/meept-scratch-rig.*/worktrees/<task> — was
// rejected and the whole regression gate went red.
func TestSessionWorkdirTaskAcceptsBoundWorktree(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	m, err := suite.Load(filepath.Join(repo, "suites/regression.json"))
	if err != nil {
		t.Fatalf("loading regression suite: %v", err)
	}
	var task suite.Task
	for _, tk := range m.Tasks {
		if tk.ID == "session-workdir-bound" {
			task = tk
		}
	}
	if task.ID == "" {
		t.Fatal("session-workdir-bound task not found in regression suite")
	}

	// The scratch root stands in for the rig's worktrees/ dir. Its name is
	// deliberately NOT any of the shapes the old pattern knew.
	scratch := t.TempDir()
	var boundDir string
	sock, _ := routingFake(t, `[]`, false, false, "", func(wt string, _ map[string]any) {
		if wt == "" {
			t.Error("runner never registered a project/worktree")
			return
		}
		boundDir = wt
		if err := os.WriteFile(filepath.Join(wt, "cwd.txt"), []byte(wt+"\n"), 0o600); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("MEEPT_BENCH_SOCKET", sock)

	r, err := New(Options{RepoPath: repo, ScratchRoot: scratch, OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	row, wt := r.RunTask(ctx, m, task, 1)
	if wt != nil && row != nil && row.WorktreeKept {
		t.Cleanup(func() {
			_ = execGit(t, repo, "worktree", "remove", "--force", row.WorktreePath)
		})
	}
	if !row.Passed {
		t.Fatalf("correct bound worktree rejected: verdict=%s checks=%+v", row.Verdict, row.Checks)
	}
	// The bound dir must be a per-attempt worktree under the scratch root —
	// the shape the old pattern did not know. Assert it so this test cannot
	// pass vacuously if the runner ever stops binding a worktree. Compare
	// resolved paths: isolate.NewManager canonicalises the scratch root with
	// EvalSymlinks, so on macOS the bound dir reads /private/var/... while
	// t.TempDir() hands back /var/... — the very /tmp vs /private/tmp alias
	// class this task is about.
	resolvedScratch, err := filepath.EvalSymlinks(scratch)
	if err != nil {
		t.Fatalf("resolving scratch root: %v", err)
	}
	if !strings.HasPrefix(boundDir, resolvedScratch) {
		t.Fatalf("bound dir %q is not under the scratch root %q", boundDir, resolvedScratch)
	}
	if !strings.HasSuffix(boundDir, "regression-session-workdir-bound-a1") {
		t.Fatalf("bound dir %q is not the per-attempt task worktree", boundDir)
	}
	t.Logf("bound worktree %q accepted by checker", boundDir)
}

// TestSessionWorkdirTaskRejectsBenchRepoRoot is the other direction: if the
// agent reports the bench repo CHECKOUT (the main checkout the task worktree
// forked from), the task must FAIL. The fake agent writes cwd.txt = that
// checkout instead of its bound worktree.
//
// Note: when the bench itself runs from a linked worktree, the checkout the
// worktree forked from is the main checkout (what `git rev-parse
// --git-common-dir`'s parent names), not the worktree — so that is what the
// agent must be caught reporting.
func TestSessionWorkdirTaskRejectsBenchRepoRoot(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	m, err := suite.Load(filepath.Join(repo, "suites/regression.json"))
	if err != nil {
		t.Fatalf("loading regression suite: %v", err)
	}
	var task suite.Task
	for _, tk := range m.Tasks {
		if tk.ID == "session-workdir-bound" {
			task = tk
		}
	}
	if task.ID == "" {
		t.Fatal("session-workdir-bound task not found in regression suite")
	}

	// The wrong answer: the main checkout the task worktree forked from.
	mainCheckout := repo
	if out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output(); err == nil {
		if common := strings.TrimSpace(string(out)); common != "" {
			mainCheckout = filepath.Dir(common)
		}
	}
	if mainCheckout == repo {
		t.Skip("bench is not running from a linked worktree; the repo-root answer is the worktree itself")
	}

	sock, _ := routingFake(t, `[]`, false, false, "", func(wt string, _ map[string]any) {
		if wt == "" {
			t.Error("runner never registered a project/worktree")
			return
		}
		if err := os.WriteFile(filepath.Join(wt, "cwd.txt"), []byte(mainCheckout+"\n"), 0o600); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("MEEPT_BENCH_SOCKET", sock)

	r, err := New(Options{RepoPath: repo, ScratchRoot: t.TempDir(), OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	row, wt := r.RunTask(ctx, m, task, 1)
	if wt != nil && row != nil && row.WorktreeKept {
		t.Cleanup(func() {
			_ = execGit(t, repo, "worktree", "remove", "--force", row.WorktreePath)
		})
	}
	if row.Passed {
		t.Fatalf("bench repo checkout accepted: checks=%+v", row.Checks)
	}
	if row.Verdict == "error" {
		t.Fatalf("expected a graded fail, got an error row: %+v", row)
	}
	t.Logf("bench repo checkout %q rejected as intended: %s", mainCheckout, row.Verdict)
}
