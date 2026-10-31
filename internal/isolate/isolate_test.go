package isolate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func TestNewManagerAbsAndSymlink(t *testing.T) {
	// Test that NewManager resolves absolute paths.
	tempDir := t.TempDir()
	relPath := filepath.Join("relative", "path")
	// Create the relative path structure.
	if err := os.MkdirAll(relPath, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(relPath)

	mgr, err := NewManager(relPath)
	if err != nil {
		t.Fatalf("NewManager(%q) = %v", relPath, err)
	}
	// Root should be absolute.
	if !filepath.IsAbs(mgr.Root) {
		t.Errorf("Root is not absolute: %q", mgr.Root)
	}

	// Test symlink validation: create a symlink that points outside tempDir.
	outer := t.TempDir()
	symPath := filepath.Join(tempDir, "sym")
	if err := os.Symlink(outer, symPath); err != nil {
		t.Fatal(err)
	}
	// This should work (symlink is valid, just not inside repoPath yet).
	mgr2, err := NewManager(symPath)
	if err != nil {
		t.Fatalf("NewManager(symlink) = %v", err)
	}
	_ = mgr2

	// Test NewManager on a NON-EXISTENT nested path.
	freshPath := filepath.Join(tempDir, "fresh", "deep", "nested")
	mgr3, err := NewManager(freshPath)
	if err != nil {
		t.Fatalf("NewManager(non-existent nested path) = %v", err)
	}
	if !filepath.IsAbs(mgr3.Root) {
		t.Errorf("Root is not absolute: %q", mgr3.Root)
	}
	// Verify the directory was created.
	if _, err := os.Stat(mgr3.Root); err != nil {
		t.Errorf("scratch root was not created: %v", err)
	}
}

func TestCreateCollisionGuard(t *testing.T) {
	repo := initRepo(t)
	scratch := t.TempDir()
	mgr, err := NewManager(scratch)
	if err != nil {
		t.Fatal(err)
	}

	// Create a worktree.
	wt, err := mgr.Create(repo, "task1")
	if err != nil {
		t.Fatal(err)
	}

	// Write .bench-owner file with a live PID (current process).
	ownerFile := filepath.Join(wt.Path, ".bench-owner")
	if err := os.WriteFile(ownerFile, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}

	// Try to create another worktree with the same name - should fail.
	_, err = mgr.Create(repo, "task1")
	if err == nil {
		t.Error("expected error for collision, got nil")
	}

	// Clean up.
	wt.Teardown()
}

func TestCreateReclaimDeadOwner(t *testing.T) {
	repo := initRepo(t)
	scratch := t.TempDir()
	mgr, err := NewManager(scratch)
	if err != nil {
		t.Fatal(err)
	}

	wt, err := mgr.Create(repo, "task2")
	if err != nil {
		t.Fatal(err)
	}

	// Write .bench-owner with a dead PID (0 is invalid).
	ownerFile := filepath.Join(wt.Path, ".bench-owner")
	if err := os.WriteFile(ownerFile, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create should succeed (reclaim stale owner).
	wt2, err := mgr.Create(repo, "task2")
	if err != nil {
		t.Fatalf("expected reclaim to succeed, got %v", err)
	}
	_ = wt2

	wt.Teardown()
}