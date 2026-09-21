package suite

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// TestCampaignPhase2ManifestLoads pins the generated campaign-phase2 suite
// (suites/campaign-phase2.json, produced by tools/gen-phase2-suite.py) against
// schema drift and pins the bench timeout policy: per-task timeout_seconds
// must scale with intent class — multi-step orchestration intents (quickplan,
// plan) get 600s because those turns legitimately run 4-9 minutes on the
// local chain, while single-step intents keep 300s. The phase-2 run of
// 2026-09-20 showed a flat 240s policy clipping 7/48 turns across five
// intents while completed turns peaked at ~145s.
func TestCampaignPhase2ManifestLoads(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := strings.TrimSuffix(thisFile, "/internal/suite/campaign_phase2_manifest_test.go")
	path := repoRoot + "/suites/campaign-phase2.json"

	// The suite is generated from the local (untracked) replay-gold corpus
	// and is gitignored, so it only exists on machines that ran the
	// generator. Policy is still pinned universally by
	// TestTimeoutPolicyGeneratorPinsPolicy against the committed generator.
	if _, err := os.Stat(path); err != nil {
		t.Skipf("suite %s not generated on this machine; run tools/gen-phase2-suite.py", path)
	}

	m, err := Load(path)
	if err != nil {
		t.Fatalf("loading %s: %v", path, err)
	}
	if m.Suite != "campaign-phase2" {
		t.Errorf("suite name = %q, want %q", m.Suite, "campaign-phase2")
	}
	if len(m.Tasks) != 48 {
		t.Errorf("task count = %d, want 48 (frozen corpus size)", len(m.Tasks))
	}

	const multiTimeout = 600
	const singleTimeout = 300
	multiIntents := map[string]bool{"quickplan": true, "plan": true}

	seen := make(map[string]bool, len(m.Tasks))
	for _, task := range m.Tasks {
		if seen[task.ID] {
			t.Errorf("duplicate id %q", task.ID)
		}
		seen[task.ID] = true
		if task.TimeoutS <= 0 {
			t.Errorf("task %s: timeout_seconds not set", task.ID)
			continue
		}
		if !hasTag(task, "replay") || !hasTag(task, "phase2") {
			t.Errorf("task %s: missing replay/phase2 tag", task.ID)
		}
		// The task's intent tag is the intent class; timeout must match.
		intent := ""
		for _, tg := range task.Tags {
			if tg == "replay" || tg == "phase2" || tg == "known-failure" {
				continue
			}
			intent = tg
			break
		}
		if intent == "" {
			t.Errorf("task %s: no intent tag", task.ID)
			continue
		}
		want := singleTimeout
		if multiIntents[intent] {
			want = multiTimeout
		}
		if task.TimeoutS != want {
			t.Errorf("task %s (intent %s): timeout_seconds = %d, want %d (timeout policy: multi-step 600s, single-step 300s)",
				task.ID, intent, task.TimeoutS, want)
		}
		if task.ExpectIntent != "" && task.ExpectIntent != intent {
			t.Errorf("task %s: expect_intent %q != intent tag %q", task.ID, task.ExpectIntent, intent)
		}
	}
}

// TestTimeoutPolicyGeneratorPinsPolicy documents the policy in code: a policy
// change must touch tools/gen-phase2-suite.py, and this test fails until the
// generator and the committed suite are regenerated in the same commit. The
// multi-step window must exceed DefaultLivenessTimeout (300s) plus dispatch
// latency so a healthy 240-540s turn is never clipped by the task context
// before the client's own liveness window fires; the 2026-09-20 phase-2
// evidence includes a quickplan turn that only finished at ~540s.
func TestTimeoutPolicyGeneratorPinsPolicy(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := strings.TrimSuffix(thisFile, "/internal/suite/campaign_phase2_manifest_test.go")
	b, err := os.ReadFile(repoRoot + "/tools/gen-phase2-suite.py")
	if err != nil {
		t.Fatalf("reading generator: %v", err)
	}
	src := string(b)

	if !strings.Contains(src, `MULTI_STEP_INTENTS = {"quickplan", "plan"}`) {
		t.Errorf("generator: MULTI_STEP_INTENTS policy line missing/changed")
	}
	if !strings.Contains(src, "MULTI_STEP_TIMEOUT = 600") {
		t.Errorf("generator: MULTI_STEP_TIMEOUT must be 600s (multi-step turns run 4-9 min)")
	}
	if !strings.Contains(src, "SINGLE_STEP_TIMEOUT = 300") {
		t.Errorf("generator: SINGLE_STEP_TIMEOUT must be 300s (completed single-step turns peak ~145s)")
	}
}
