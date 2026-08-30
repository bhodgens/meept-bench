package checkers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// writeWorktree writes raw bytes into a fresh worktree for checker tests.
func writeWorktree(t *testing.T, name string, content []byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestFileContainsAnchoredWithTrailingNewline: Go RE2 anchors ^/$ to the TEXT,
// not per line. "42\n" must still satisfy "^42$" — the checker trims the
// trailing newline before matching so single-line files with a trailing \n
// (what every editor and most tools produce) are not spurious failures.
func TestFileContainsAnchoredWithTrailingNewline(t *testing.T) {
	dir := writeWorktree(t, "answer.txt", []byte("42\n"))
	r := Run(context.Background(), suite.Check{Type: "file_contains", Files: []string{"answer.txt"}, Pattern: "^42$"}, dir, "", nil)
	if !r.Passed {
		t.Fatalf("want pass for \"42\\n\" against ^42$, got %+v", r)
	}
}

func TestFileContainsAnchoredExactNoNewline(t *testing.T) {
	dir := writeWorktree(t, "answer.txt", []byte("42"))
	r := Run(context.Background(), suite.Check{Type: "file_contains", Files: []string{"answer.txt"}, Pattern: "^42$"}, dir, "", nil)
	if !r.Passed {
		t.Fatalf("want pass for \"42\" against ^42$, got %+v", r)
	}
}

func TestFileContainsAnchoredRejectsWrongContent(t *testing.T) {
	for _, content := range []string{"41\n", "142\n", "42 43\n", " 42\n"} {
		dir := writeWorktree(t, "answer.txt", []byte(content))
		r := Run(context.Background(), suite.Check{Type: "file_contains", Files: []string{"answer.txt"}, Pattern: "^42$"}, dir, "", nil)
		if r.Passed {
			t.Fatalf("want fail for %q against ^42$, got %+v", content, r)
		}
	}
}

// TestFileContainsAnchoredMultiline: with trailing-newline trimming, ^/$ bind
// to the whole (trimmed) text. A number buried mid-file must NOT match ^42$.
func TestFileContainsAnchoredMultiline(t *testing.T) {
	dir := writeWorktree(t, "answer.txt", []byte("line1\n42\nline3\n"))
	r := Run(context.Background(), suite.Check{Type: "file_contains", Files: []string{"answer.txt"}, Pattern: "^42$"}, dir, "", nil)
	if r.Passed {
		t.Fatalf("want fail: ^42$ should not match a middle line under text-anchoring, got %+v", r)
	}
}
