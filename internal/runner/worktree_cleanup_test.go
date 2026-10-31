package runner

// bughunt 2026-10-31 (deferred finding 2.1): RunSuite must not leak
// scratch worktrees when the context is cancelled mid-suite. The
// deferred cleanup calls teardownUnkeptWorktrees; this test pins the
// helper's real behavior against live git worktrees.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bhodgens/meept-bench/internal/isolate"
)

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "bench@test")
	run("config", "user.name", "bench")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	return dir
}

func TestTeardownUnkeptWorktrees(t *testing.T) {
	repo := initGitRepo(t)
	scratch := t.TempDir()
	mgr, err := isolate.NewManager(scratch)
	if err != nil {
		t.Fatal(err)
	}

	wt1, err := mgr.Create(repo, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	wt2, err := mgr.Create(repo, "task-b")
	if err != nil {
		t.Fatal(err)
	}
	wt2.Keep = true // simulates KeepFailed/--keep on a failed attempt

	teardownUnkeptWorktrees([]*isolate.Worktree{wt1, wt2, nil})

	if _, err := os.Stat(wt1.Path); !os.IsNotExist(err) {
		t.Errorf("unkept worktree %s still exists after teardown", wt1.Path)
	}
	if _, err := os.Stat(wt2.Path); err != nil {
		t.Errorf("kept worktree %s was torn down: %v", wt2.Path, err)
	}
}
