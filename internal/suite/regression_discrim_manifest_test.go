package suite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegressionDiscrimManifestPortable pins suites/regression-discrim.json:
//
//  1. It loads through suite.Load — which for every trusted_python checker
//     resolves the script relative to the manifest dir (os.OpenRoot, no
//     escape) and REJECTS a sha256 mismatch. A load success is therefore the
//     hash-pin proof, not just a JSON parse.
//  2. No checker embeds a developer absolute path. The file previously carried
//     /Users/caimlas/git/meept-bench fallbacks in three harness-integrity
//     probes; those silently no-op on every other checkout, which is worse
//     than red — a gate that passes without measuring.
//  3. The three harness-integrity probes and the session-workdir probe are all
//     trusted_python, so the portable env-var mechanism (MEEPT_BENCH_TRANSCRIPT_DIR /
//     MEEPT_BENCH_RESULTS_DIR, exported by internal/checkers exitZero) is the
//     only path resolution they can use.
func TestRegressionDiscrimManifestPortable(t *testing.T) {
	path := filepath.Join("..", "..", "suites", "regression-discrim.json")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("loading %s (hash pin included): %v", path, err)
	}

	// Load does not expose scriptCode to this package's test... it does:
	// scriptCode is same-package. Assert every trusted_python checker got a
	// non-empty verified body and a manifest-relative script path.
	for _, task := range m.Tasks {
		for j, c := range task.Checkers {
			for _, arg := range c.Command {
				if strings.Contains(arg, "/Users/") {
					t.Errorf("task %s check[%d]: checker argument embeds a developer home path: %q", task.ID, j, arg)
				}
			}
			if c.Type == "trusted_python" {
				if c.TrustedScript() == "" {
					t.Errorf("task %s check[%d]: trusted script body not loaded", task.ID, j)
				}
				if c.Script == "" || strings.Contains(c.Script, "..") || filepath.IsAbs(c.Script) {
					t.Errorf("task %s check[%d]: script path %q must be manifest-relative without ..", task.ID, j, c.Script)
				}
			}
		}
	}

	byID := map[string]Task{}
	for _, task := range m.Tasks {
		byID[task.ID] = task
	}
	for _, taskID := range []string{"session-workdir-bound", "bus-trace-arrives", "harness-cost-row-present", "transcript-completeness"} {
		task, ok := byID[taskID]
		if !ok {
			t.Fatalf("task %s missing from regression-discrim.json", taskID)
		}
		var tp int
		for _, c := range task.Checkers {
			if c.Type == "trusted_python" {
				tp++
			}
			if c.Type == "exit_zero" {
				t.Errorf("task %s still carries an exit_zero checker (hardcoded-path class): %v", taskID, c.Command)
			}
		}
		if tp == 0 {
			t.Errorf("task %s: no trusted_python probe found", taskID)
		}
	}

	// The raw JSON must contain no developer path anywhere (belt over the
	// Command-arg loop above, which skips non-command fields).
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "/Users/") {
		t.Errorf("regression-discrim.json still embeds a /Users/ path")
	}
	var probe json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("raw JSON parse: %v", err)
	}
}
