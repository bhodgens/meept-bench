package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bhodgens/meept-bench/internal/checkers"
	"github.com/bhodgens/meept-bench/internal/results"
	"github.com/bhodgens/meept-bench/internal/suite"
)

// TestTranscriptWriteAndCheckerAgreeOnDir pins the invariant the transcript
// probes in suites/regression.json depend on: the directory the runner writes
// transcripts into is the SAME directory it exports to checkers as
// MEEPT_BENCH_TRANSCRIPT_DIR.
//
// Before this was pinned, writeTranscript resolved the out dir WITHOUT the
// suite name (results/transcripts) while the checker env got the suite-scoped
// path (results/<suite>/transcripts) whenever --out was empty. The transcript
// probe then read a directory that never existed and failed with "transcript
// missing" on a healthy run — a false red, not a real regression.
func TestTranscriptWriteAndCheckerAgreeOnDir(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outDir  string // "" -> resolve under a temp cwd; absolute -> used as-is
		suiteID string
	}{
		{"default-layout", "", "regression"},
		{"explicit-out", "", "regression"},
		{"relative-out", "results/wave1", "regression"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Hermetic: every relative resolution lands under this temp cwd,
			// never the package directory.
			cwd := t.TempDir()
			chdir(t, cwd)
			outDir := tc.outDir
			if tc.name == "explicit-out" {
				outDir = filepath.Join(cwd, "custom")
			}
			r := &Runner{opt: Options{OutDir: outDir}}
			stem := tc.suiteID + "-probe-a1"
			r.writeTranscript(tc.suiteID, stem, &results.Transcript{
				Suite: tc.suiteID, TaskID: "probe", Attempt: 1,
				Prompt: "p", FinalReply: "r",
			})

			// The transcript dir the runner exports to checkers, and the file
			// the runner's own write path targets.
			exported := filepath.Join(r.outDirOr(tc.suiteID), "transcripts")
			written := filepath.Join(exported, stem+".json")
			if _, err := os.Stat(written); err != nil {
				t.Fatalf("runner wrote no transcript where checkers are told to look (%s): %v", written, err)
			}

			// A checker with cwd = an unrelated worktree reads the transcript
			// through the exported env var, using the same one-liner shape as
			// suites/regression.json "transcript-completeness".
			c := suite.Check{Type: "exit_zero", Command: []string{"sh", "-c",
				`test -f "$MEEPT_BENCH_TRANSCRIPT_DIR/` + stem + `.json"`}}
			res := checkers.Run(context.Background(), c, t.TempDir(), "",
				nil, checkers.WithTranscriptDir(exported), checkers.WithResultsDir(r.outDirOr(tc.suiteID)))
			if !res.Passed {
				t.Errorf("checker could not read the written transcript: %+v", res)
			}
		})
	}
}

// chdir switches the process cwd for the duration of a test. Go 1.24+
// disallows t.Chdir in parallel tests, so this helper keeps the intent
// explicit and restore-safe for the serial tests that use it.
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

// TestResultsDirMatchesRowWriter pins the other half: the dir exported as
// MEEPT_BENCH_RESULTS_DIR is where RunSuite appends results.jsonl.
func TestResultsDirMatchesRowWriter(t *testing.T) {
	// Hermetic: a relative default would otherwise land in the package dir.
	chdir(t, t.TempDir())
	r := &Runner{opt: Options{OutDir: t.TempDir()}}
	rowPath := filepath.Join(r.outDirOr("regression"), "results.jsonl")
	if err := appendRow(rowPath, results.Row{Suite: "regression", TaskID: "probe", Verdict: "pass", Passed: true}); err != nil {
		t.Fatal(err)
	}

	c := suite.Check{Type: "exit_zero", Command: []string{"sh", "-c",
		`test -f "$MEEPT_BENCH_RESULTS_DIR/results.jsonl"`}}
	res := checkers.Run(context.Background(), c, t.TempDir(), "",
		nil, checkers.WithResultsDir(r.outDirOr("regression")))
	if !res.Passed {
		t.Errorf("results.jsonl not visible at MEEPT_BENCH_RESULTS_DIR: %+v", res)
	}
}