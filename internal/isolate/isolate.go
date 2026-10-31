// Package isolate gives each attempt a fresh git worktree under a scratch root.
package isolate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// Worktree is a per-attempt checkout.
type Worktree struct {
	Root     string // scratch root containing all worktrees
	RepoPath string
	Path     string // the worktree dir
	Branch   string
	Keep     bool
}

// Manager creates and tears down worktrees.
type Manager struct{ Root string }

// NewManager creates the scratch root if missing.
func NewManager(root string) (*Manager, error) {
	if root == "" {
		return nil, fmt.Errorf("scratch root is required")
	}
	// Resolve to absolute path.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// Ensure the directory exists before resolving symlinks.
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return nil, err
	}
	// Resolve symlinks.
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("scratch root symlink resolution: %w", err)
	}
	// Ensure the resolved root is not inside the repoPath tree.
	// This check is performed by the caller (runner.New) who passes RepoPath.
	// We store the resolved path for later use.
	// (MkdirAll already created absRoot; resolvedRoot is the canonical path.)
	return &Manager{Root: resolvedRoot}, nil
}

func run(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", args[0], err, trim(out))
	}
	return out, nil
}

func trim(b []byte) string {
	if len(b) > 300 {
		b = b[:300]
	}
	return string(b)
}

// Create makes a detached worktree from repoPath named after the task.
// The worktree starts at HEAD; tasks mutate it in isolation.
func (m *Manager) Create(repoPath, name string) (*Worktree, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(m.Root, sanitize(name))

	// Concurrent worktree collision guard: if the dir exists, check if it's
	// owned by another process via a .bench-owner file.
	if _, err := os.Stat(dir); err == nil {
		ownerFile := filepath.Join(dir, ".bench-owner")
		if data, err := os.ReadFile(ownerFile); err == nil {
			if pid, err := parsePID(string(data)); err == nil {
				// Check if the owner PID is still alive.
				if kill(pid, 0) == nil {
					// Another process owns this worktree — refuse.
					return nil, fmt.Errorf("worktree %q already exists and is owned by live PID %d; use --scratch to pick a different root or wait for the other run to complete", dir, pid)
				}
				// Owner is dead — reclaim by removing the stale worktree.
				if err := os.RemoveAll(dir); err != nil {
					return nil, err
				}
			}
		}
	}

	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	branch := "bench/" + sanitize(name)
	// A prior kept-failed attempt may have left this branch behind; prune
	// dead worktree registrations first or git refuses -D on it.
	_, _ = run(abs, "worktree", "prune")
	_, _ = run(abs, "branch", "-D", branch)
	if _, err := run(abs, "worktree", "add", "-b", branch, dir, "HEAD"); err != nil {
		return nil, err
	}
	// Write owner PID for concurrent collision detection.
	ownerFile := filepath.Join(dir, ".bench-owner")
	if err := os.WriteFile(ownerFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644); err != nil {
		// Non-fatal; the worktree is still usable, just less protected.
	}
	return &Worktree{Root: m.Root, RepoPath: abs, Path: dir, Branch: branch}, nil
}

// parsePID parses a PID from a string.
func parsePID(s string) (int, error) {
	var pid int
	_, err := fmt.Sscanf(s, "%d", &pid)
	return pid, err
}

// kill sends signal 0 to pid (probe only).
func kill(pid int, sig int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.Signal(sig))
}

// Teardown removes the worktree unless Keep is set.
func (w *Worktree) Teardown() error {
	if w.Keep {
		return nil
	}
	_, _ = run(w.RepoPath, "worktree", "remove", "--force", w.Path)
	_, err := run(w.RepoPath, "branch", "-D", w.Branch)
	return err
}

// Git runs an arbitrary git command inside the worktree and returns output.
func (w *Worktree) Git(args ...string) (string, error) {
	out, err := run(w.Path, args...)
	return string(out), err
}

func sanitize(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	s := string(out)
	if s == "" {
		s = "task"
	}
	return s
}
