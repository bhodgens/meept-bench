package daemonclient

// bughunt 2026-10-31 (deferred finding 3.1): a wedged/absent daemon must
// fail fast, not hang the bench forever.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDialTimeoutOnMissingSocket(t *testing.T) {
	tmpDir := t.TempDir()
	c := New(filepath.Join(tmpDir, "nonexistent.sock"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	err := c.Ping(ctx)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error dialing non-existent socket, got nil")
	}
	// 3 attempts (1 + maxRetries=2), each bounded by DefaultDialTimeout,
	// plus 250+500ms backoff. A missing socket fails instantly on unix,
	// so the real bound is the backoff sum plus overhead.
	if elapsed > 16*time.Second {
		t.Errorf("dial not bounded: elapsed %v", elapsed)
	}
	if !strings.Contains(err.Error(), "dial") {
		t.Errorf("error %q should name the dial failure", err)
	}
}
