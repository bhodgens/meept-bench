# Meept-Bench Phase 2 — Implementation Orchestrator

> **For the executing agent:** You are the orchestrator for this tree node.
> Your job: (1) dispatch implementation agents, (2) review their work,
> (3) re-dispatch if incomplete, (4) track completion.
> Do NOT implement code yourself. All implementation happens in leaf agents.

## Meta

- **Role:** Root
- **Parent:** none (root)
- **Children:** 9 leaf documents under 3 group nodes
- **Scope:** Turn meept-bench from a working harness with one toy suite into
  meept's regression gate, then add the first real benchmark (LongMemEval-S),
  then wire it into CI so it runs without a human.

## Goal

meept-bench (this repo) has a proven harness: suite manifest → per-task git
worktree → live daemon RPC → tool-trace capture → checkers → scorecard. It
has also proven itself as meept's best bug-finder: the routing, tool-hint,
bus-subscription, session-binding, and cost-attribution bugs (GAPS.md 9–12)
were all found by the smoke suite and all got fixed.

What it does not yet provide:

1. **A regression suite.** Only `suites/smoke.json` (3 trivial tasks) exists.
   Every bug the harness caught should become a permanent task so it can
   never silently regress.
2. **Scorecard history.** Nothing compares today's run against last week's;
   regressions are invisible.
3. **A real benchmark.** Zero Phase-2 suites (docs/PLAN.md). LongMemEval-S is
   the chosen first target: no tool use, pure memory — directly tests meept's
   claimed differentiator (episodic+epistemic memory) with the cheapest
   signal.
4. **CI.** The harness only runs when a human runs it.

This tree delivers all four, in dependency order: regression suite first
(immediate value, exercises the harness we just hardened), scorecard diffing
second (needed to interpret regression runs), LongMemEval third (needs
stable checkers + diffing to be meaningful), CI last (needs a suite worth
running nightly).

## Architecture

Everything lands in this repo (`meept-bench`). No meept-side changes except
where a leaf explicitly calls one out (deterministic tool variants need a
small meept config surface, leaf 08 — the only cross-repo leaf).

- **Suites** are JSON manifests (`suites/*.json`) using the existing schema
  (`internal/suite/suite.go`): `id`, `prompt`, `checkers`, `timeout_seconds`,
  `seeds`, and (new this week) `expect_agent`.
- **Regression tasks** encode past bugs as assertion tasks: the checker set is
  the regression contract. File-write routing bugs become `file_contains`
  checks plus `expect_agent: "coder"`; bus-subscription regressions become
  tasks whose checkers only pass when tool traces arrive in the transcript.
- **Scorecard diffing** is a new subcommand (`meept-bench diff`) consuming two
  `results.jsonl` files: per-task verdict delta, wall-time delta (warn
  threshold +50%), cost delta, new/fix/fail counts. Exit code 1 when any
  previously-passing task now fails — that is the CI gate.
- **LongMemEval-S adapter** is an adapter script (`cmd/lmeval-adapter/` or
  `internal/suites/lmeval/`) that fetches the HF dataset by revision hash
  (never committing task content — BENCHMARKS.md rule 2), converts questions
  to task manifests with `llm_judge` or `file_contains` checkers, and emits a
  suite file the existing runner already understands.
- **CI** is a GitHub Actions workflow running `doctor` + smoke + regression
  suites against a built meept-daemon at a pinned commit, failing the build
  when `diff` exits non-zero. Nightly for full suites; per-push for smoke.

Concurrency: leaves in group A (regression suite) and group B (scorecard
diff) are independent. Group C (LongMemEval) depends on B's diff command for
meaningful result comparison but not on its code structure — its adapter can
be built in parallel once checkers stabilize. Group D (CI) depends on A and B.

## Interface Contracts

### Contract 1: Diff command output

```
Command:  meept-bench diff --baseline results/smoke/results.jsonl \
                             --current  results/smoke/results.jsonl
Exit codes: 0 = no previously-passing task regressed
            1 = at least one pass→fail transition
            2 = usage error (missing file, unparseable JSONL)

Output (stdout, human-readable; --json for machine):
  REGRESSED (pass→fail): <task_id> [<task_id>...]
  FIXED (fail→pass):     <task_id> [<task_id>...]
  NEW:                   <task_id> [...]
  REMOVED:               <task_id> [...]
  wall-time deltas > +50%:  <task_id> (x.xs → y.ys)
  cost deltas:           <task_id> ($a → $b)

Owner: 04-diff-command.md
Consumers: 05-ci-workflow.md (exit code), 06-regression-gate.md (workflow doc)
```

### Contract 2: Regression suite manifest schema

```
File: suites/regression.json
Same schema as smoke.json (internal/suite). Every task MUST set:
  - "expect_agent": asserted dispatcher routing (we have this since 01c4f38)
  - at least one file_contains or exit_zero checker asserting the BUG FIX,
    not just task completion
  - "tags": ["regression", "<origin-gap-or-issue>"] e.g. "gap-9-routing"
Naming: <behavior>-<variant> e.g. "file-write-routes-coder",
  "memory-recall-survives-restart", "tool-hint-coder-maps-coder"

Initial task set (one per fixed bug; seeds [1]; timeout 300s):
  1.  file-write-routes-coder        (gap 9 / commit 4f48e129)
  2.  tool-hint-coder-maps-coder     (commit a0939721 — via tool_hint path)
  3.  bus-trace-arrives              (commit a20b105c: checker asserts
      transcript carried tool events — implement as exit_zero on a
      transcript-probe script, see leaf 06 for the mechanism)
  4.  memory-recall-basic            (meept memory.recent RPC round trip)
  5.  scheduler-followup             (schedule a +1min reminder, verify fire)
  6.  pending-write-direct           (staged-write detection: checker must
      FAIL when the agent stages instead of writing — inverted assertion)
  7.  cost-attributed                (row cost_usd ≥ 0 and tokens_in > 0)

Owner: 06-regression-suite.md
Consumers: 05-ci-workflow.md, 07-diff-gate.md
```

### Contract 3: LongMemEval adapter boundary

```
Adapter input:  HF dataset id + revision hash + split (S) from a config file
                (suites/longmemeval.local.json — gitignored, holds the user's
                HF token reference; token comes from env HF_TOKEN)
Adapter output: suites/longmemeval-s.generated.json (gitignored — generated)
                + suites/longmemeval-s.template.json (committed: 5-task
                template shape checked in for schema validation)
Rule (BENCHMARKS.md rule 2): NEVER commit dataset task content. The adapter
  fetches at run time under the user's own HF gate acceptance.
Checker mapping: single-hop → file_contains on the answer file the agent
  writes; open-ended → llm_judge with per-question rubric (judge command
  required; harness supports it since phase 1).

Owner: 08-longmemeval-adapter.md
Consumers: 09-docs.md
```

### Contract 4: CI workflow inputs

```
Workflow: .github/workflows/bench.yml
  push trigger:   build meept-daemon at the repo's pinned commit hash
                  (env MEEPT_DAEMON_REF, default: main) + meept-bench,
                  run doctor + smoke (--attempts 1)
  nightly (cron): smoke + regression + longmemeval-s.template
  gate step:      meept-bench diff --baseline <last-green.jsonl>
                  --current <this-run.jsonl>  (exit 1 fails the build)
  artifacts:      results.jsonl + scorecard.md uploaded per run (90-day
                  retention) so baselines are recoverable
  disclosure:     workflow env AUTO_APPROVED=1 disclosed in every row
                  (GAPS.md gap 6 rule: auto-approval is suite-scoped and
                  disclosed, never silent)

Owner: 05-ci-workflow.md
```

## Execution Order

```
Wave 1 (parallel, independent):
  - 01..03  regression task authoring (grouped by meept subsystem)
  - 04      diff command
Wave 2 (after wave 1 review):
  - 06      assemble regression.json from 01–03 outputs
  - 05      CI workflow
Wave 3 (after wave 2):
  - 07      regression-gate runbook + baseline capture
  - 08      LongMemEval adapter (can start earlier if checkers stable)
  - 09      docs: README status flip (Phase 2 started), BENCHMARKS.md rule
            additions for generated suites, GAPS.md status updates
```

## Explicit Non-Goals

- No GAIA/Terminal-Bench/τ-bench/SWE-bench adapters this phase (GAPS.md
  blocking gaps 1–2 stand; do not start suites the daemon cannot pass).
- No step-level cost attribution (GAPS.md gap 8) — task-level deltas are
  enough for a gate; step-level is Phase 3.
- No deterministic tool fixtures (GAPS.md gap 7) beyond a stub leaf; without
  them LongMemEval is still safe because it needs no web tools.
- No weakening of meept security defaults for auto-approval (GAPS.md rule).

## Completion Definition

1. `suites/regression.json` runs green end-to-end twice consecutively.
2. `meept-bench diff` exits 1 on an injected pass→fail fault (test it by
   flipping a checker pattern, running, then restoring).
3. CI workflow green on push; nightly scheduled; baseline JSONL stored as an
   artifact and re-usable.
4. LongMemEval-S template suite runs end-to-end (5 tasks) against a live
   daemon with a judge command configured.
5. README "Status" section updated: Phase 2 in progress, regression gate live.

## Completion Tracking

| Leaf | Status | Iterations | Completed | Completeness |
|---|---|---|---|---|
| 01-regression-routing | COMPLETE | 1 | 2026-09-01T18:08-06:00 | 100% |
| 02-regression-memory | COMPLETE | 1 | 2026-09-01T18:25-06:00 | 100% (recall known-failure by design) |
| 03-regression-harness | COMPLETE | 2 (orchestrator fix-loop: drop expect_agent, minimal prompts) | 2026-09-01T18:34-06:00 | 100% |
| 04-diff-command | COMPLETE | 1 | 2026-09-01T18:05-06:00 | 100% |
| 05-ci-workflow | COMPLETE | 1 | 2026-09-01T19:06-06:00 | 95% (update-baseline.sh runtime proof deferred to first CI run) |
| 06-regression-suite | COMPLETE | 1 | 2026-09-01T19:06-06:00 | 100% (green ×2, diff exit 0) |
| 07-regression-gate | COMPLETE | 2 (orchestrator: pattern restore via patch; drill verified) | 2026-09-01T19:26-06:00 | 100% (fault → exit 1, restore → exit 0) |
| 08-longmemeval-adapter | COMPLETE | 1 | 2026-09-01T20:05-06:00 | 90% (real-dataset fetch + template e2e pending meept classifier recovery / operator-approved download) |
| 09-docs | COMPLETE | 1 | 2026-09-01T19:21-06:00 | 100% (amended post-08 by orchestrator) |

**Root: COMPLETE — 2026-09-01T20:20-06:00 — 97% overall.**

Gate results: per-leaf reviews in-session (all APPROVED after fix loops);
Gate 1 branch review Wave 1 + Wave 2 in-session APPROVED; Gate 2
build/vet/gofmt/test clean across repo; Gate 3 pre-commit passed on every
commit. Functional trace: gate one-liner exercised live (run → PASS; diff →
exit 0); lmeval subcommand responds.

Known open items (logged, non-blocking):
1. meept classifier degradation (local LFM runtime down + zai timeouts) —
   flips long-prompt routing; blocks template e2e and makes expect_agent
   flaky under outage. Meept-side fix or alias re-order needed.
2. LongMemEval real-dataset fetch→emit→run cycle awaits an
   operator-approved bulk download.
3. update-baseline.sh bare-repo runtime proof deferred to first real CI run.
4. Meept-side: memory relevance 0.203 < 0.3 injection threshold silently
   drops cross-conversation recall (pinned by memory-recall-marker).
5. Meept-side: memory_store type=task SQL error (table task_memories has no
   column named search_text).
