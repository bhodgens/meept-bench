# Assemble regression.json + Runbook - Implementation Leaf

> **For the implementing agent:** You are the implementer for this leaf.
> Implement ALL tasks below. Do NOT commit — the orchestrator handles all
> git operations after review.

## Meta

- **Parent:** ../master.md
- **Scope:** Assemble `suites/regression.json` from the verified fragments
  (01, 02, 03), run the full suite green twice, and write the operator
  runbook.
- **Dependencies:** 01-regression-routing, 02-regression-memory,
  03-regression-harness (all landed).
- **Estimated Context:** 50K
- **Concurrency Group:** B

## Goal

One committed suite file, `suites/regression.json`, that runs every
regression task and passes end-to-end twice consecutively — the definition
of a stable gate. Plus `docs/RUNBOOK.md` telling a future operator how to
run it, read failures, and update baselines.

## Context

- Fragments land in `suites/fragments/*.json` from leaves 01–03. They may be
  full Manifests or bare task arrays — read each file and normalize.
- Manifest schema: `{"suite": "regression", "tasks": [...]}` with the fields
  per internal/suite/suite.go. Validate with the loader: loading the file
  via the bench binary catches schema errors (run with `--task NOMATCH` to
  validate-then-run-zero-tasks, or write a tiny go test calling suite.Load).
- The memory-recall pair from leaf 02 may be marked known-failure with the
  documented xfail shape. Keep such tasks LAST in the array with a leading
  comment-equivalent (JSON has no comments — encode in tags) so a red
  known-failure is visually distinct in scorecards.

## Tasks

### Task 1: merge fragments

Assemble `suites/regression.json`:
- `"suite": "regression"`
- description: "Meept regression gate. One task per fixed bug (see tags for
  origin commits/gaps). Green twice consecutively = stable."
- All tasks from all fragments, ordered: routing tasks, memory tasks,
  harness-integrity tasks, known-failures last.
- Normalize: every task has timeout_seconds, seeds [1], tags including
  "regression", expect_agent where leaf specs set it.
- Sanity: no duplicate IDs; every checker pattern compiles
  (suite.Validate does regex compile since 61ff240 — a load test catches
  bad patterns).

Add `internal/suite/regression_manifest_test.go`: a test that loads
`suites/regression.json` via suite.Load relative to the repo root (use
runtime.Caller or a ../../ path) and asserts: loads clean, ≥6 tasks, every
task tagged "regression", no duplicate IDs. This pins the committed suite
against schema drift.

### Task 2: run green twice

1. `go build -o /tmp/meept-bench-bin ./cmd/meept-bench`
2. Run 1: `/tmp/meept-bench-bin run --suite suites/regression.json --out
   results/regression-run1` — capture the output.
3. Run 2: same with `--out results/regression-run2`.
4. Both runs: all non-known-failure tasks PASS. Compare run1 vs run2 with
   the diff command (leaf 04): expect no regressed, no fixed (all same
   verdicts), exit 0.
5. If any task is flaky (passes run1 fails run2 or vice versa): run it 3
   more times in isolation. Persistently flaky tasks: keep but tag
   "flaky" and note in the runbook; do not delete findings.

### Task 3: runbook

Write `docs/RUNBOOK.md` (concise, operational):
- Prereqs: running meept-daemon, provider key configured, build commands.
- Commands: doctor, run smoke, run regression, diff two runs, keep-failed
  postmortem (where worktrees land, how to inspect transcripts).
- Failure triage flowchart: routing mismatch (grep daemon log
  `Dispatched request agent=`), checker pattern miss (inspect kept worktree
  file), empty tool_trace (bus subscription — see a20b105c), daemon
  unreachable (socket path via MEEPT_BENCH_SOCKET).
- Baseline updates: after a GREEN full run, refresh
  `results/baseline/regression.jsonl` (local convention until CI lands).
- Known-failures section: list any xfail tasks with reasons.
- Cost note: rows show $0.00 on free-tier providers; deltas still work.

## Verification

- `go test ./internal/suite/` passes with the new manifest test.
- Two consecutive green runs (evidence in your report: the run outputs).
- diff run1 run2 exits 0.
- RUNBOOK.md covers all six triage paths with concrete commands.

## Report

Report: final task list (ids + tags), both run outputs, diff output,
runbook path, deviations.
