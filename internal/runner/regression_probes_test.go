package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bhodgens/meept-bench/internal/checkers"
	"github.com/bhodgens/meept-bench/internal/results"
	"github.com/bhodgens/meept-bench/internal/suite"
)

// regressionCheckerOpts builds the option list exactly as RunTask does. Kept
// in one place so these tests cannot drift from the production wiring they
// are supposed to prove.
func regressionCheckerOpts(r *Runner, suiteID string) []checkers.RunOption {
	runOutDir := r.outDirOr(suiteID)
	return []checkers.RunOption{
		checkers.WithTranscriptDir(filepath.Join(runOutDir, "transcripts")),
		checkers.WithResultsDir(runOutDir),
	}
}

// checkerByName returns the manifest checker with the given type for a task.
func checkerByName(t *testing.T, m *suite.Manifest, taskID, checkerType string) suite.Check {
	t.Helper()
	for _, task := range m.Tasks {
		if task.ID != taskID {
			continue
		}
		for _, c := range task.Checkers {
			if c.Type == checkerType {
				return c
			}
		}
	}
	t.Fatalf("no %s checker on task %s", checkerType, taskID)
	return suite.Check{}
}

// TestRegressionHarnessIntegrityProbesPortable drives the three real
// harness-integrity checkers from suites/regression.json through the real
// runner option wiring and asserts each one's INTACT intent — not just that it
// runs. A portability fix that quietly turned a red into a green would be
// worse than the hardcoded path it replaced.
func TestRegressionHarnessIntegrityProbesPortable(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	m, err := suite.Load(filepath.Join(repo, "suites", "regression.json"))
	if err != nil {
		t.Fatalf("load regression.json: %v", err)
	}

	// Each of the three tasks must carry its transcript/results probe as a
	// trusted_python checker: real code pinned by sha256, not an inline shell
	// string with a baked-in absolute path.
	for _, taskID := range []string{"bus-trace-arrives", "harness-cost-row-present", "transcript-completeness"} {
		if c := checkerByName(t, m, taskID, "trusted_python"); c.Type != "trusted_python" {
			t.Errorf("task %s: no trusted_python probe found", taskID)
		}
		// No checker on these tasks may embed an absolute path from one
		// developer's checkout.
		for _, c := range checkerByNameAll(t, m, taskID) {
			for _, arg := range c.Command {
				if strings.Contains(arg, "/Users/") {
					t.Errorf("task %s: checker argument embeds an absolute home path: %q", taskID, arg)
				}
			}
		}
	}

	cases := []struct {
		name    string
		taskID  string
		suiteID string
	}{
		{"default-out", "transcript-completeness", "regression"},
		{"explicit-out", "transcript-completeness", "regression"},
		{"bus-trace", "bus-trace-arrives", "regression"},
		{"cost-row", "harness-cost-row-present", "regression"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			chdir(t, cwd)
			outDir := ""
			if tc.name == "explicit-out" {
				outDir = filepath.Join(cwd, "elsewhere")
			}
			r := &Runner{opt: Options{OutDir: outDir}}
			runOutDir := r.outDirOr(tc.suiteID)

			// Materialize what the runner would have written for a HEALTHY
			// run. bus-trace-arrives additionally needs a non-empty tool
			// trace, since asserting that trace is the whole probe.
			healthy := &results.Transcript{
				Suite: tc.suiteID, TaskID: tc.taskID, Attempt: 1,
				Prompt: "do the thing", FinalReply: "done",
			}
			if tc.taskID == "bus-trace-arrives" {
				healthy.ToolTrace = []results.ToolEvent{
					{Topic: "tool.execution.complete", Type: "tool.execution.complete"},
				}
			}
			stem := writeTranscriptFor(t, r, tc.suiteID, tc.taskID, 1, healthy)

			c := checkerByName(t, m, tc.taskID, "trusted_python")
			res := checkers.Run(context.Background(), c, t.TempDir(), "final answer", nil,
				regressionCheckerOpts(r, tc.suiteID)...)
			if !res.Passed {
				t.Fatalf("healthy run rejected: %+v", res)
			}
			if !strings.HasPrefix(res.Detail, "OK ") {
				t.Errorf("detail does not record the probe's success line: %q", res.Detail)
			}
			if _, err := os.Stat(filepath.Join(runOutDir, "transcripts", stem+".json")); err != nil {
				t.Errorf("transcript not written under the exported dir: %v", err)
			}
		})
	}
}

// TestRegressionProbesFailOnTheirOriginalCondition is the anti-weakening guard:
// each probe must still FAIL on exactly the condition it was written to catch.
// If one of these ever passes, the checker has been neutered.
func TestRegressionProbesFailOnTheirOriginalCondition(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	m, err := suite.Load(filepath.Join(repo, "suites", "regression.json"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		taskID  string
		suiteID string
		seed    func(t *testing.T, transcriptPath, resultsPath string)
		wantSub string
	}{
		{
			// a20b105c: empty tool_trace == the bus subscription regressed.
			name:    "bus-trace-empty-tool-trace-fails",
			taskID:  "bus-trace-arrives",
			suiteID: "regression",
			seed: func(t *testing.T, transcriptPath, _ string) {
				writeJSON(t, transcriptPath, map[string]any{
					"tool_trace": []any{},
				})
			},
			wantSub: "tool_trace empty",
		},
		{
			// transcript-completeness: a missing final_reply is a half-turn.
			name:    "transcript-missing-final-reply-fails",
			taskID:  "transcript-completeness",
			suiteID: "regression",
			seed: func(t *testing.T, transcriptPath, _ string) {
				writeJSON(t, transcriptPath, map[string]any{
					"prompt": "p", "final_reply": "",
				})
			},
			wantSub: "final_reply missing",
		},
		{
			// transcript-completeness: a missing prompt is equally a half-turn.
			name:    "transcript-missing-prompt-fails",
			taskID:  "transcript-completeness",
			suiteID: "regression",
			seed: func(t *testing.T, transcriptPath, _ string) {
				writeJSON(t, transcriptPath, map[string]any{
					"prompt": "", "final_reply": "r",
				})
			},
			wantSub: "prompt missing",
		},
		{
			// 01c4f38: a PASSING row with negative tokens_in is fiction.
			name:    "cost-negative-tokens-in-fails",
			taskID:  "harness-cost-row-present",
			suiteID: "regression",
			seed: func(t *testing.T, _, resultsPath string) {
				writeRows(t, resultsPath, []map[string]any{
					{"task_id": "ok-task", "verdict": "pass", "tokens_in": 120},
					{"task_id": "bad-task", "verdict": "pass", "tokens_in": -5},
				})
			},
			wantSub: "negative tokens_in",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			chdir(t, cwd)
			r := &Runner{}
			runOutDir := r.outDirOr(tc.suiteID)
			// The runner names transcripts "<suite>-<task>-a<N>.json"; use the
			// production writer so the probe sees exactly what it would see live.
			transcriptPath := filepath.Join(runOutDir, "transcripts",
				writeTranscriptFor(t, r, tc.suiteID, tc.taskID, 1, &results.Transcript{
					Suite: tc.suiteID, TaskID: tc.taskID, Attempt: 1,
					Prompt: "p", FinalReply: "r",
				})+".json")
			tc.seed(t, transcriptPath, filepath.Join(runOutDir, "results.jsonl"))

			c := checkerByName(t, m, tc.taskID, "trusted_python")
			res := checkers.Run(context.Background(), c, t.TempDir(), "", nil,
				regressionCheckerOpts(r, tc.suiteID)...)
			if res.Passed {
				t.Fatalf("checker PASSED on the condition it must catch; detail = %q", res.Detail)
			}
			if !strings.Contains(res.Detail, tc.wantSub) {
				t.Errorf("failure detail %q does not explain %q", res.Detail, tc.wantSub)
			}
		})
	}
}

// TestRegressionProbesFailClosedWithoutEnv: the failure mode the hardcoded
// path hid. With no env var the old checker fell through to a results/** glob;
// if that glob matched nothing it crashed with IndexError, and if the glob was
// skipped entirely (the cost probe's `[ -f ... ] || exit 0`) it PASSED WITHOUT
// CHECKING ANYTHING. Every probe must now refuse to render a verdict.
func TestRegressionProbesFailClosedWithoutEnv(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	m, err := suite.Load(filepath.Join(repo, "suites", "regression.json"))
	if err != nil {
		t.Fatal(err)
	}

	for _, taskID := range []string{"bus-trace-arrives", "harness-cost-row-present", "transcript-completeness"} {
		t.Run(taskID, func(t *testing.T) {
			c := checkerByName(t, m, taskID, "trusted_python")
			// No RunOption: neither dir is exported. Also clear any inherited
			// value so this cannot pass by reading the developer's real results.
			t.Setenv("MEEPT_BENCH_TRANSCRIPT_DIR", "")
			t.Setenv("MEEPT_BENCH_RESULTS_DIR", "")

			res := checkers.Run(context.Background(), c, t.TempDir(), "", nil)
			if res.Passed {
				t.Fatalf("checker passed with no run dir exported (silent no-op verdict); detail = %q", res.Detail)
			}
			if !strings.Contains(res.Detail, "is unset") {
				t.Errorf("failure detail %q should name the missing env var", res.Detail)
			}
		})
	}
}

// TestRegressionProbesIgnoreForeignRunDir: a transcript or results row left by
// a PREVIOUS run must not satisfy a probe. This is what the old
// results/**-glob fallback got wrong and why the env var exists.
func TestRegressionProbesIgnoreForeignRunDir(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	m, err := suite.Load(filepath.Join(repo, "suites", "regression.json"))
	if err != nil {
		t.Fatal(err)
	}

	cwd := t.TempDir()
	chdir(t, cwd)
	// A sibling run dir, fully populated and healthy, that this run must ignore.
	foreign := t.TempDir()
	if err := os.MkdirAll(filepath.Join(foreign, "transcripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(foreign, "transcripts",
		"regression-transcript-completeness-a1.json"),
		map[string]any{"prompt": "p", "final_reply": "r"})

	r := &Runner{}
	c := checkerByName(t, m, "transcript-completeness", "trusted_python")
	res := checkers.Run(context.Background(), c, t.TempDir(), "", nil,
		regressionCheckerOpts(r, "regression")...)
	if res.Passed {
		t.Fatalf("checker satisfied by another run's transcript; detail = %q", res.Detail)
	}
	if !strings.Contains(res.Detail, "no ") {
		t.Errorf("detail %q should say this run has no transcript", res.Detail)
	}
}

// TestManifestCheckerHashPinsScript: the trusted_python contract — the manifest
// names the script AND its digest, so editing the checker without updating the
// manifest fails at load. This is what makes "checker code in a .py file"
// trustworthy rather than a new way to smuggle untested code in.
func TestManifestCheckerHashPinsScript(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "suites", "regression.json")
	m, err := suite.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	c := checkerByName(t, m, "transcript-completeness", "trusted_python")
	if c.TrustedScript() == "" {
		t.Fatal("trusted script not loaded")
	}
	// The recorded hash must be the digest of the bytes on disk.
	raw, err := os.ReadFile(filepath.Join(repo, "suites", c.Script))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != c.Hash {
		t.Errorf("manifest sha256 = %q, file digest = %q", c.Hash, got)
	}
	if strings.Contains(c.Script, "..") || filepath.IsAbs(c.Script) {
		t.Errorf("script path must be manifest-relative, got %q", c.Script)
	}
}

// --- helpers ---------------------------------------------------------------

func checkerByNameAll(t *testing.T, m *suite.Manifest, taskID string) []suite.Check {
	t.Helper()
	for _, task := range m.Tasks {
		if task.ID == taskID {
			return task.Checkers
		}
	}
	t.Fatalf("task %s not found", taskID)
	return nil
}

// writeTranscriptFor writes a transcript under the name the runner's own
// writeTranscript would use for (suiteID, taskID, attempt), which is
// "<suiteID>-<taskID>-a<N>.json". Checkers glob that stem, so a helper that
// invents any other name silently tests nothing.
func writeTranscriptFor(t *testing.T, r *Runner, suiteID, taskID string, attempt int, tr *results.Transcript) string {
	t.Helper()
	stem := suiteID + "-" + taskID + "-a" + strconv.Itoa(attempt)
	r.writeTranscript(suiteID, stem, tr)
	return stem
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeRows(t *testing.T, path string, rows []map[string]any) {
	t.Helper()
	var sb strings.Builder
	for _, row := range rows {
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}