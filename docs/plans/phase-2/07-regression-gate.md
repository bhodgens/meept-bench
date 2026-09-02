# Regression-Gate Operations & Baseline - Implementation Leaf

> **For the implementing agent:** You are the implementer for this leaf.
> Implement ALL tasks below. Do NOT commit — the orchestrator handles all
> git operations after review.

## Meta

- **Parent:** ../master.md
- **Scope:** Prove the diff gate has teeth (injected-fault test), capture
  the first committed baseline, define the gate policy.
- **Dependencies:** 04 (diff command), 06 (regression.json green twice).
- **Estimated Context:** 30K
- **Concurrency Group:** C2

## Goal

A gate nobody has tested failing is decoration. This leaf injects a fault,
proves `diff` catches it, restores, then captures the green baseline that
CI and local runs compare against.

## Tasks

### Task 1: injected-fault drill

1. Copy the green regression results to
   `results/baseline/regression.jsonl` (this becomes the committed
   baseline).
2. Inject a fault: edit ONE checker pattern in
   `suites/regression.json` (e.g. "dispatch works" → "dispatch works!!")
   — run the suite with `--out results/regression-fault`; that task fails.
3. Run `meept-bench diff --baseline results/baseline/regression.jsonl
   --current results/regression-fault/results.jsonl` — must exit 1 and
   list the task as REGRESSED.
4. Restore the pattern, re-run the single task, confirm diff is clean
   against baseline.
Record every step's output in the report.

### Task 2: flake policy doc

Add a `## Flake policy` section to docs/RUNBOOK.md:
- A task failing <1 in 5 local runs = flaky: tag "flaky", keep in suite,
  gate ignores via `--ignore-tags flaky` support IF trivially addable to
  the runner (check: suite.Task.Tags exists; runner Select honors filter —
  if adding a tag filter to `run` is >20 lines, defer to a follow-up and
  document the manual procedure instead).
- A task failing deterministically = real regression: diff gate fires;
  fix meept or fix the task, never the baseline.

### Task 3: baseline commit convention

Document (RUNBOOK) + implement: baseline JSONLs live at
`results/baseline/<suite>.jsonl` and ARE committed (small files; they pin
the gate). .gitignore currently has `/results/` — add negation
`!results/baseline/` and `!results/baseline/**`. Verify with
`git check-ignore -v results/baseline/regression.jsonl` → must NOT be
ignored, and `git status` shows the file as addable.

### Task 4: gate wiring for local use

One-liner in RUNBOOK operators run after any meept change:
```
meept-bench run --suite suites/regression.json && \
  meept-bench diff --baseline results/baseline/regression.jsonl \
                   --current results/regression/results.jsonl
```
Exit 0 = safe. Verify this exact command sequence works end-to-end.

## Verification

- The drill (Task 1) shows exit 1 with the faulted task, exit 0 restored.
- check-ignore proves the baseline path is committable.
- The Task 4 sequence exits 0 against a green daemon.

## Report

Report: drill transcripts (before/after diff outputs), baseline file path
+ row count, flake-policy text, deviations.
