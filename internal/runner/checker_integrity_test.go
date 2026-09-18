package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bhodgens/meept-bench/internal/suite"
)

func TestRoutingRepairCheckerReplacementCannotPass(t *testing.T) {
	repo, _ := filepath.Abs("../..")
	m, err := suite.Load(filepath.Join(repo, "suites/routing-repair.json"))
	if err != nil {
		t.Fatal(err)
	}
	task := m.Select("media-url-as-data", nil)[0]
	entries := `[{"session_id":"conv-route","agent_id":"coder","intent_type":"code","classifier_method":"llm"}]`
	sock, _ := routingFake(t, entries, false, false, "", func(wt string, _ map[string]any) {
		// Model can edit fixtures, but must not replace its evaluator. No test is produced.
		if err := os.WriteFile(filepath.Join(wt, ".routing-checks.py"), []byte("raise SystemExit(0)\n"), 0644); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("MEEPT_BENCH_SOCKET", sock)
	r, err := New(Options{RepoPath: repo, ScratchRoot: t.TempDir(), OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	row := r.RunTask(context.Background(), m, task, 1)
	if row.Passed || row.Verdict != "fail" || len(row.Checks) != 1 || passedCheck(row.Checks[0]) {
		t.Fatalf("replacement fabricated success: %+v", row)
	}
}

func TestRoutingRepairTrustedCheckerAcceptsRealFixtureWork(t *testing.T) {
	repo, _ := filepath.Abs("../..")
	m, err := suite.Load(filepath.Join(repo, "suites/routing-repair.json"))
	if err != nil {
		t.Fatal(err)
	}
	task := m.Select("media-url-as-data", nil)[0]
	entries := `[{"session_id":"conv-route","agent_id":"coder","intent_type":"code","classifier_method":"llm"}]`
	sock, _ := routingFake(t, entries, false, false, "", func(wt string, _ map[string]any) {
		test := "import unittest\nfrom url_value import video_id\nclass URLTest(unittest.TestCase):\n def test_url(self):\n  self.assertEqual(video_id('https://www.youtube.com/watch?v=BaW_jenozKc'),'BaW_jenozKc')\n"
		if err := os.WriteFile(filepath.Join(wt, "test_url_value.py"), []byte(test), 0644); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("MEEPT_BENCH_SOCKET", sock)
	r, err := New(Options{RepoPath: repo, ScratchRoot: t.TempDir(), OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	row := r.RunTask(context.Background(), m, task, 1)
	if !row.Passed {
		t.Fatalf("real fixture work rejected: %+v", row)
	}
}
