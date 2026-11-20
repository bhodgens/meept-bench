# 07-BENCH TEST-GATE REPORT (tests only, RED)

Worktree: /Users/caimlas/git/meept-bench/.hermes/worktrees/usability-acceptance
Base HEAD: 5ac2e50 (clean; suites/usability-probe.json was pre-staged untracked).
Nothing committed, nothing git-added. Go 1.27.3, gofmt clean, ASCII-only,
no `_ = ` added lines, no implementation code in the worktree.

## Files created (4 test files, tests only)

| File | Task | Tests |
|------|------|-------|
| internal/checkers/terminal_delivery_test.go | T1 | TestTerminalDeliveryChecker (18 subtests incl. mutation) |
| internal/checkers/reply_quality_test.go | T2 | TestReplyQualityJudgeLint, TestNoAnswerLeak |
| internal/suite/usability_suite_test.go | T3 | TestSuiteLoadsUsabilityProbe, TestSuiteLoadsValidateAcceptsAcceptanceCheckers, TestSuiteLoadsValidateRejectsVacuousChecker |
| internal/checkers/plan_artifact_test.go | T3 | TestPlanArtifactChecker (12 subtests incl. artifact-removal mutation) |

## RED output per task (verify commands from the leaf, run at HEAD)

### T1: go test ./internal/checkers/ -run TestTerminalDeliveryChecker -count=1
```
--- FAIL: TestTerminalDeliveryChecker (0.00s)
    terminal_delivery_test.go:124: RED: terminal_delivery checker is not implemented (Run detail: "unknown checker type")
FAIL	github.com/bhodgens/meept-bench/internal/checkers	0.226s
```
RED reason: checker absent (Run type switch has no terminal_delivery case).

### T2: go test ./internal/checkers/ -run 'TestReplyQualityJudgeLint|TestNoAnswerLeak' -count=1
```
--- FAIL: TestReplyQualityJudgeLint (0.00s)
    reply_quality_test.go:83: RED: reply-quality rubric set is not implemented (no llm_judge rubrics in suites/usability-probe.json)
--- FAIL: TestNoAnswerLeak (0.00s)
    reply_quality_test.go:201: RED: reply-quality rubric set is not implemented (no llm_judge rubrics in suites/usability-probe.json)
FAIL	github.com/bhodgens/meept-bench/internal/checkers	0.224s
```
RED reason: rubric set absent (suite has zero llm_judge checks today).

### T3: go test ./internal/suite/ ./internal/checkers/ -run 'TestSuiteLoads|TestPlanArtifactChecker' -count=1
```
--- FAIL: TestSuiteLoadsUsabilityProbe
    RED: task u-context-two-step has no terminal_delivery checker (R6 acceptance pin)
    RED: task u-steer-midrun has no terminal_delivery checker (R6 acceptance pin)
    RED: task u-route-research-write has no terminal_delivery checker (R6 acceptance pin)
    RED: task u-fail-nonexistent-read has no terminal_delivery checker (R6 acceptance pin)
    RED: task u-plan-mode-request check[0] is a vacuous exit_zero(true); replace with a real outcome checker (I8)
    RED: task u-plan-mode-request has no terminal_delivery checker (R6 acceptance pin)
    RED: task u-direct-explicit has no terminal_delivery checker (R6 acceptance pin)
    RED: u-plan-mode-request has no plan_artifact/llm_judge checker (vacuous exit_zero(true) must be replaced)
--- FAIL: TestSuiteLoadsValidateAcceptsAcceptanceCheckers
    RED: Validate rejects acceptance checker type "terminal_delivery": unknown checker type
    RED: Validate rejects acceptance checker type "plan_artifact": unknown checker type
--- FAIL: TestSuiteLoadsValidateRejectsVacuousChecker
    RED: Validate accepts vacuous exit_zero [true] / [sh -c true] / [sh -c :]
--- FAIL: TestPlanArtifactChecker (0.00s)
    plan_artifact_test.go:88: RED: plan_artifact checker is not implemented (Run detail: "unknown checker type")
FAIL	github.com/bhodgens/meept-bench/internal/suite
FAIL	github.com/bhodgens/meept-bench/internal/checkers
```
RED reasons: vacuous checker still present; plan checker absent; Validate
does not know the new types and does not refuse vacuous exit_zero(true).

## Mutation evidence (T1, load-bearing pin)

The gate worktree stays tests-only, so mutation verification ran on a
scratch copy at /tmp/07-gate-mutation-proof (rsync of the worktree; the
scratch reference implementation NEVER lands in the gate tree):

1. Contract-faithful reference implementation of terminal_delivery
   (current-run dir only, extra.task_id association, C1 status set,
   parked exclusion, deadline default 600s, distinct-turn_id counting,
   accepted:false actionable-note rule) wired into the scratch Run switch:
   `go test ./internal/checkers/ -run TestTerminalDeliveryChecker -count=1 -v`
   -> PASS, all 18 subtests including mutation_remove_terminal_flips_to_fail.
   This proves the gate is satisfiable by a correct implementation (no
   impossible assertions) and that the mutation subtest passes only when
   the checker genuinely detects terminal removal.
2. Sabotage arm: same scratch checker replaced with unconditional
   `return true, "always pass (sabotaged)", nil`:
   - 10+ subtests FAIL (missing_terminal, parked_only, late, C1 set,
     turn_id, distinct-terminals, silent rejection, no-dir, ...).
   - Mutation subtest specifically:
     `terminal_delivery_test.go:456: MUTATION NOT DETECTED: removing the
     terminal must flip the checker to FAIL: {Passed:true Detail:always
     pass (sabotaged)}` -> FAIL.
   - Restoring the faithful implementation flips the mutation subtest
     back to PASS (byte-restored from backup, re-run verified).

The in-test mutation itself: passing fixture (progress event + completed
terminal) passes; the SAME fixture rewritten with ONLY the turn.terminal
event removed must FAIL. Both directions proven above.

T3 carries a secondary mutation: removing plan.md from a passing
plan_artifact fixture must flip it to FAIL (same discipline).

## Contract pins for the 01 implementer (documented in test headers)

- terminal_delivery / plan_artifact: new Run switch cases; suite.Validate
  must accept both types. Association via extra.task_id (sanctioned Check
  extension channel); missing association FAILS, never passes.
- Transcript source: WithTranscriptDir option, else MEEPT_BENCH_TRANSCRIPT_DIR
  env; NEVER a bare results/** glob (decoy-fixture subtests prove it).
- Terminal satisfaction: turn.terminal trace events with C1 closed-set
  status completed|failed|timeout (parked excluded), non-empty turn_id,
  within extra.deadline_seconds (default 600) of transcript start;
  DISTINCT turn_id count >= submitted turns (primary + accepted:true
  turns). accepted:false turns need a non-empty actionable note (surfaced
  in Detail as "rejection: ..."); silent rejections FAIL.
- plan_artifact PASS evidence (any): non-empty plan.md/PLAN.md at worktree
  root or non-empty *.md under plans/; plan lifecycle bus evidence in the
  current-run trace (plan.created / plan.draft / plan.sealed /
  plan.submitting - topics verified at meept HEAD); or judge-verified
  plan reply when the check carries a rubric. Prose alone never passes.
- Reply-quality rubrics delivered as llm_judge checks in the hardened
  suite; rubric union must name empty / envelope / "## review:" / job /
  context defect classes; answer-leak lint (extra.expected) must refuse
  leaked rubrics with the judge NEVER called (call-counting stub judges).
- Suite hardening: >=6 tasks, unique IDs, no vacuous exit_zero (true,
  sh -c true, sh -c :), terminal_delivery on EVERY task (R6), real
  plan_artifact/llm_judge on u-plan-mode-request, Validate rejects
  vacuous checkers at load time. ALL 6 prompts + 2 turn messages pinned
  byte-identical (frozen).

## No existing tests weakened

Full package run: `go test ./internal/checkers/ ./internal/suite/
./internal/results/ -count=1` -> ONLY the 7 new gate tests FAIL; every
pre-existing test passes (results package ok). `go build ./...` OK.
gofmt -l clean on all four files; ASCII verified byte-wise.

## Deviations

1. T2 rubric discovery is suite-driven (llm_judge checks in
   suites/usability-probe.json) rather than referencing a new
   ReplyQualityRubric() symbol: an undefined-symbol reference would be a
   BUILD failure, which would also block T1/T3 in the same package. The
   suite is the sanctioned rubric delivery surface per the 01 leaf; the
   01 implementer may expose a helper, but the gate reads the manifest.
2. Mutation verification used a /tmp scratch copy with a throwaway
   reference implementation (both faithful and sabotaged arms), because
   the gate worktree must contain tests only. Scratch artifacts live at
   /tmp/07-gate-mutation-proof and /tmp/td-scratch-good.go; nothing was
   written into the gate tree beyond the four test files.
3. terminal_delivery pinned on every task (01 leaf: "terminal_delivery +
   reply_quality on all tasks"); reply-quality coverage is pinned at the
   rubric-set level (defect-class union + lint sweep), not per-task, to
   avoid over-constraining which tasks carry which rubric.
4. plan_artifact no-dir-no-env case fails closed even when a worktree
   artifact exists (association evidence required) - fail-closed choice
   documented in the test for the implementer.

DO NOT COMMIT honored: git status shows only the 4 untracked test files
plus the pre-existing untracked suites/usability-probe.json.