package isolate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initRepo(t *testing.T) string {
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

func TestCreateAndTeardown(t *testing.T) {
	repo := initRepo(t)
	scratch := t.TempDir()
	mgr, err := NewManager(scratch)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := mgr.Create(repo, "suite-task-a1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Git("rev-parse", "HEAD"); err != nil {
		t.Fatalf("worktree broken: %v", err)
	}
	if err := wt.Teardown(); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if _, err := wt.Git("rev-parse", "HEAD"); err == nil {
		t.Fatal("worktree should be gone")
	}
}

func TestSanitize(t *testing.T) {
	got := sanitize("GAIA/L2 task #7!")
	if got != "GAIA-L2-task--7-" {
		t.Fatalf("got %q", got)
	}
}
