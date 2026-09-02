# Docs & Status Sync - Implementation Leaf

> **For the implementing agent:** You are the implementer for this leaf.
> Implement ALL tasks below. Do NOT commit — the orchestrator handles all
> git operations after review.

## Meta

- **Parent:** ../master.md
- **Scope:** Documentation truth-sync after Phase 2 lands: README, PLAN,
  BENCHMARKS, GAPS.
- **Dependencies:** 06 (regression suite live), 08 (LongMemEval adapter).
  Author LAST — this leaf records what actually shipped, so stale claims
  are worse than late.
- **Estimated Context:** 30K
- **Concurrency Group:** D (final)

## Goal

The repo's docs promised Phase 2; the docs must now tell the truth about
what landed, what passed, and what remains. This repo exists to publish
honest evidence — the same standard applies to its own README.

## Tasks

### Task 1: README status flip

- "Status: Phase 1 harness implemented" → reflect reality: regression gate
  live (suites/regression.json, N tasks), scorecard diff command, CI
  workflow, LongMemEval-S template + adapter.
- Quick start: add the regression + diff commands.
- Keep the design-rules section untouched (still true).

### Task 2: PLAN phase-status update

docs/PLAN.md: mark Phase 1 complete (it is), Phase 2 in progress with what
landed (regression gate, diff, CI, LongMemEval template) and what didn't
(GAIA etc.). Do NOT rewrite history — append a "Phase 2 status (date)"
section.

### Task 3: BENCHMARKS.md generated-suite rule

Add rule 5: generated suites (adapter output, e.g. longmemeval-s.generated)
are never committed; committed template suites must use synthetic fixtures,
never upstream task content; every generated task embeds the dataset
revision hash in tags/meta.

### Task 4: GAPS.md status sync

Update gap statuses honestly:
- Gap 5 (steering over RPC): note what phase-1 verification found.
- Gap 6 (auto-approval): note the disclosure mechanism now exists in rows.
- Gap 9 (classifier hijack): FIXED (commits 4f48e129 + tests); smoke suite
  covers it; regression suite pins it.
- Gaps 1–4, 7–8: unchanged, still open.
Do NOT mark gaps fixed that aren't verified fixed by a passing task.

### Task 5: RUNBOOK cross-link

README links docs/RUNBOOK.md (from leaf 06) under a "Operating the gate"
heading.

## Verification

- Every claim in the edited docs maps to an artifact that exists in the
  tree (suite file, command, workflow file, commit). Grep-check each.
- No doc claims a Phase-2 number (pass rates) that wasn't produced by a
  real run recorded in the report of the relevant leaf — cite the run
  instead of inventing numbers.

## Report

Report: files changed, the artifact-claim mapping table, deviations.
