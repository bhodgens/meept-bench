package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// End-to-end harness check with deliberately broken routing evidence, driven
// over the real client path (unix socket, real Runner, real worktree, real
// checker subprocesses). The wire fake stands in for the meept daemon; no
// production daemon, model call, or network is involved. It captures the
// runner's concrete evidence output and verifies checks.py's assertions
// against that exact transcript.
func TestRoutingRepairBrokenEvidenceIntegration(t *testing.T) {
	for _, identity := range []string{"conv-route", "step-task-distinct-step-1"} {
		t.Run(identity, func(t *testing.T) { routingRepairMediaEvidence(t, identity) })
	}
}

func routingRepairMediaEvidence(t *testing.T, identity string) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	outDir := t.TempDir()
	prompt := "Fetch the transcript of https://www.youtube.com/watch?v=BaW_jenozKc and save it to video-transcript.txt."

	// The outcome checker ships with the suite and is hash-pinned into the
	// loader memory, never the worktree.
	checkSrc, err := os.ReadFile(filepath.Join(repo, "suites", "routing-repair-data", "checks.py"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "routing-repair-data"), 0o755); err != nil {
		t.Fatal(err)
	}
	checkDst := filepath.Join(tmp, "routing-repair-data", "checks.py")
	if err := os.WriteFile(checkDst, checkSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(checkSrc)
	manifest := map[string]any{
		"suite":    "routing-repair",
		"internal": true,
		"tasks": []any{map[string]any{
			"id": "media-transcript-control", "prompt": prompt, "timeout_seconds": 5,
			"expect_agent": "coder", "expect_intent": "code",
			"forbidden_classification_methods": []string{"short_message_guard"},

			"checkers": []any{
				map[string]any{"type": "file_contains", "file": "route-artifact.txt", "pattern": "routing-test-marker"},
				map[string]any{"type": "trusted_python", "script": "routing-repair-data/checks.py", "sha256": hex.EncodeToString(sum[:]), "command": []string{"media"}},
			},
		}},
	}
	raw, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(tmp, "suite.json")
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := suite.Load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	entries := `[{"session_id":"conv-route","agent_id":"writer","intent_type":"chat","classifier_method":"short_message_guard"}]`
	transcriptName := "routing-repair-media-transcript-control-a1"
	toolPayload := map[string]any{
		"tool_name": "transcript_fetch", "success": true, "cached": false,
		"conversation_id": identity,
	}
	sock, _ := routingFakeTool(t, entries, false, false, "", toolPayload, func(wt string, params map[string]any) {
		if params["message"] != prompt {
			t.Errorf("prompt changed: %q", params["message"])
		}
		// The wire fake performs the (simulated) ingestion and publishes the
		// same payload shape the real executor emits for transcript_fetch.
		if err := os.WriteFile(filepath.Join(wt, "video-transcript.txt"),
			[]byte("sample video transcript used by the wire fake; contains the word test for the checker"), 0o644); err != nil {
			t.Error(err)
		}
	})

	t.Setenv("MEEPT_BENCH_SOCKET", sock)
	// The in-attempt checker subprocess reads these; MEEPT_ROUTING_RUN_DIR must
	// name THIS run's fresh output directory.
	t.Setenv("MEEPT_ROUTING_RUN_DIR", outDir)
	r, err := New(Options{RepoPath: repo, ScratchRoot: t.TempDir(), OutDir: outDir, KeepFailed: true, AutoApproved: true})
	if err != nil {
		t.Fatal(err)
	}
	row := r.RunTask(context.Background(), m, m.Tasks[0], 1)
	if row.WorktreeKept && row.WorktreePath != "" {
		t.Cleanup(func() {
			_ = exec.Command("git", "-C", repo, "worktree", "remove", "--force", row.WorktreePath).Run()
			_ = exec.Command("git", "-C", repo, "branch", "-D", "bench/"+transcriptName).Run()
		})
	}

	// Broken evidence must fail routing (fail, not error) while completed-work
	// checks still run: artifact passes, unsupported media fails closed.
	if row.Verdict != "fail" || row.Passed || row.ErrorKind != "routing_mismatch" {
		t.Fatalf("verdict diverged: %+v", row)
	}
	if !strings.Contains(row.ErrorDetail, "routing mismatch") {
		t.Errorf("missing mismatch detail: %q", row.ErrorDetail)
	}
	if len(row.RoutingChecks) != 3 {
		t.Fatalf("want 3 routing checks, got %+v", row.RoutingChecks)
	}
	for i, want := range []string{"expect_agent", "expect_intent", "forbidden_classification_methods"} {
		c := row.RoutingChecks[i]
		if c.Check != want || c.Status != "fail" {
			t.Errorf("routing check %d = %+v, want %s/fail", i, c, want)
		}
	}
	if len(row.Checks) != 2 {
		t.Fatalf("outcome checks not retained: %+v", row.Checks)
	}
	for i, want := range []bool{true, false} {
		if passed := passedCheck(row.Checks[i]); passed != want {
			t.Errorf("check[%d] passed=%v, want %v: %+v", i, passed, want, row.Checks[i])
		}
	}

	if !strings.Contains(fmt.Sprint(row.Checks[1]), "unsupported producer evidence") {
		t.Fatalf("media must disclose unsupported association: %+v", row.Checks[1])
	}

	// Concrete harness output on disk: the transcript must carry exactly the
	// evidence fields checks.py consumes.
	data, err := os.ReadFile(filepath.Join(outDir, "transcripts", transcriptName+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var tr map[string]any
	if err := json.Unmarshal(data, &tr); err != nil {
		t.Fatal(err)
	}
	if tr["suite"] != "routing-repair" || tr["task_id"] != "media-transcript-control" {
		t.Errorf("transcript identity: %s", data)
	}
	routing, _ := tr["routing"].(map[string]any)
	if routing == nil || routing["evidence_status"] != "available" || routing["agent_id"] != "writer" || routing["session_id"] != "conv-route" {
		t.Errorf("routing evidence: %v", routing)
	}
	if checks, _ := tr["routing_checks"].([]any); len(checks) != 3 {
		t.Errorf("transcript routing_checks: %v", tr["routing_checks"])
	}
	trace, _ := tr["tool_trace"].([]any)
	if len(trace) != 1 {
		t.Fatalf("tool_trace: %v", tr["tool_trace"])
	}
	ev, _ := trace[0].(map[string]any)
	if ev["topic"] != "tool.execution.complete" {
		t.Errorf("trace topic: %v", ev["topic"])
	}
	var payload map[string]any
	switch v := ev["payload"].(type) {
	case string:
		if err := json.Unmarshal([]byte(v), &payload); err != nil {
			t.Fatal(err)
		}
	case map[string]any:
		payload = v
	default:
		t.Fatalf("payload shape: %v", ev["payload"])
	}
	if payload["tool_name"] != "transcript_fetch" || payload["success"] != true || payload["cached"] != false || payload["conversation_id"] != identity {
		t.Errorf("payload fields checks.py relies on: %v", payload)
	}

	// Matching conversation alone must not pass media ingestion acceptance.
	// The producer lacks turn/step/thread and fetched URL/content proof.
}
