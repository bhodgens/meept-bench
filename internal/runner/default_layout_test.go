package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bhodgens/meept-bench/internal/checkers"
	"github.com/bhodgens/meept-bench/internal/results"
	"github.com/bhodgens/meept-bench/internal/suite"
)

// TestDefaultLayoutMatchesRunSuiteWrites pins the layout contract for the
// DEFAULT invocation (`meept-bench run --suite suites/regression.json` with no
// --out), which is what docs/RUNBOOK.md tells people to run.
//
// RunSuite computes its out dir as outDirOr(suite). RunTask exports the
// checker dirs from the SAME call, so the transcript probe and the row writer
// agree by construction. This test asserts that agreement end to end using
// the manifest's real checker code, rather than a hand-written re-statement of
// it, because a re-statement is exactly what drifted before.
func TestDefaultLayoutMatchesRunSuiteWrites(t *testing.T) {
	// Resolve the repo BEFORE chdir: "../.." is relative to the process CWD,
	// and the hermetic chdir below moves it into a temp dir.
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	chdir(t, cwd)

	const suiteID = "regression"
	r := &Runner{} // no Options: the default layout

	// This is the line RunSuite runs. If it and RunTask's resolution ever
	// diverge, the probe below reads an empty directory.
	runOutDir := r.outDirOr(suiteID)
	if err := os.MkdirAll(runOutDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// What RunSuite appends for the row that is being checked.
	rowPath := filepath.Join(runOutDir, "results.jsonl")
	rows := []map[string]any{{"task_id": "harness-cost-row-present", "verdict": "pass", "tokens_in": 88}}
	writeRows(t, rowPath, rows)

	// What RunTask writes for the attempt just executed.
	stem := writeTranscriptFor(t, r, suiteID, "transcript-completeness", 1, &results.Transcript{
		Suite: suiteID, TaskID: "transcript-completeness", Attempt: 1,
		Prompt: "p", FinalReply: "r",
	})

	// Load the manifest the way the runner does and drive the real checkers
	// with the real option wiring.
	m, err := suite.Load(filepath.Join(repo, "suites", "regression.json"))
	if err != nil {
		t.Fatal(err)
	}

	runOpts := []checkers.RunOption{
		checkers.WithTranscriptDir(filepath.Join(r.outDirOr(suiteID), "transcripts")),
		checkers.WithResultsDir(r.outDirOr(suiteID)),
	}

	for _, tc := range []struct {
		taskID string
		wantOK string
	}{
		{"transcript-completeness", "OK transcript-completeness"},
		{"harness-cost-row-present", "OK harness-cost-row-present"},
	} {
		c := checkerByName(t, m, tc.taskID, "trusted_python")
		res := checkers.Run(context.Background(), c, t.TempDir(), "reply", nil, runOpts...)
		if !res.Passed {
			t.Errorf("%s: default layout rejected a healthy run: %+v", tc.taskID, res)
			continue
		}
		if !strings.Contains(res.Detail, tc.wantOK) {
			t.Errorf("%s: detail %q missing %q", tc.taskID, res.Detail, tc.wantOK)
		}
	}

	// The probe must have read the transcript the writer actually produced.
	if _, err := os.Stat(filepath.Join(r.outDirOr(suiteID), "transcripts", stem+".json")); err != nil {
		t.Errorf("transcript missing at the exported path: %v", err)
	}
}