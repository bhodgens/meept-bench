// Package isolate gives each attempt a fresh git worktree under a scratch root.
package isolate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Manager{Root: root}, nil
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
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	branch := "bench/" + sanitize(name)
	if _, err := run(abs, "worktree", "add", "-b", branch, dir, "HEAD"); err != nil {
		return nil, err
	}
	return &Worktree{Root: m.Root, RepoPath: abs, Path: dir, Branch: branch}, nil
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
