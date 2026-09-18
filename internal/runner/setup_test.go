package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// The fake reads the selected fixture at chat.submit, not after completion.
// All repository mutations for this test are confined to a temporary repo.
func TestRunTaskSetsUpSelectedFilesBeforeChat(t *testing.T) {
	repo := t.TempDir()
	gitFixture(t, repo, "init")
	gitFixture(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	data := []byte("selected uncommitted fixture\n")
	source := filepath.Join(repo, "input.txt")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "not-selected.txt"), []byte("not copied"), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	raw := map[string]any{"suite": "setup", "tasks": []any{map[string]any{
		"id": "copy", "prompt": "unchanged diagnostic prompt", "timeout_seconds": 5,
		"setup_files": []any{map[string]any{"source": "input.txt", "destination": "fixture/input.txt", "sha256": hex.EncodeToString(sum[:])}},
		"checkers":    []any{map[string]any{"type": "file_contains", "file": "route-artifact.txt", "pattern": "routing-test-marker"}},
	}}}
	b, _ := json.Marshal(raw)
	manifest := filepath.Join(repo, "suite.json")
	if err := os.WriteFile(manifest, b, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := suite.Load(manifest)
	if err != nil {
		t.Fatal(err)
	}
	sock, _ := routingFake(t, `[]`, false, false, "", func(wt string, params map[string]any) {
		got, err := os.ReadFile(filepath.Join(wt, "fixture/input.txt"))
		if err != nil || string(got) != string(data) {
			t.Errorf("setup absent at chat.submit: %q %v", got, err)
		}
		if _, err := os.Stat(filepath.Join(wt, "not-selected.txt")); !os.IsNotExist(err) {
			t.Error("ambient uncommitted file copied")
		}
		if msg, _ := params["message"].(string); msg != m.Tasks[0].Prompt {
			t.Errorf("prompt changed: %+v", params)
		}
	})
	t.Setenv("MEEPT_BENCH_SOCKET", sock)
	r, err := New(Options{RepoPath: repo, ScratchRoot: t.TempDir(), OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	row := r.RunTask(context.Background(), m, m.Tasks[0], 1)
	if !row.Passed {
		t.Fatalf("setup attempt failed: %+v", row)
	}
}

func TestRunTaskSetupHashFailureStopsBeforeChat(t *testing.T) {
	repo := t.TempDir()
	gitFixture(t, repo, "init")
	gitFixture(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	path := filepath.Join(repo, "suite.json")
	raw := `{"suite":"setup","tasks":[{"id":"bad-hash","prompt":"never submit","setup_files":[{"source":"input.txt","destination":"input.txt","sha256":"` + strings.Repeat("0", 64) + `"}],"checkers":[{"type":"file_contains","file":"input.txt","pattern":"x"}]}]}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "input.txt"), []byte("changed content"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := suite.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	sock, snapshots := routingFake(t, `[]`, false, false, "")
	t.Setenv("MEEPT_BENCH_SOCKET", sock)
	r, err := New(Options{RepoPath: repo, ScratchRoot: t.TempDir(), OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	row := r.RunTask(context.Background(), m, m.Tasks[0], 1)
	if row.Verdict != "error" || !strings.Contains(row.ErrorDetail, "sha256 mismatch") || row.Passed {
		t.Fatalf("wrong failure: %+v", row)
	}
	creates, submits, _ := snapshots()
	if creates != 0 || submits != 0 {
		t.Fatalf("setup failure contacted session/chat: %d %d", creates, submits)
	}
}

func gitFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestSetupRejectsInvalidDestinationsBeforeDaemon(t *testing.T) {
	for _, dest := range []string{"../escape", "/absolute", ".git/config"} {
		t.Run(strings.ReplaceAll(dest, "/", "_"), func(t *testing.T) {
			raw := `{"suite":"setup","tasks":[{"id":"bad","prompt":"do not submit","setup_files":[{"source":"input.txt","destination":` + `"` + dest + `"` + `,"sha256":"` + strings.Repeat("0", 64) + `"}],"checkers":[{"type":"file_contains","file":"x","pattern":"x"}]}]}`
			path := filepath.Join(t.TempDir(), "suite.json")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := suite.Load(path); err == nil {
				t.Fatal("unsafe setup accepted")
			}
		})
	}
}
