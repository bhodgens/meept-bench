# Meept-Bench Phase 2 Completion + Phase 3 — Orchestrator Plan

> Created 2026-09-04. Supersedes remaining open items in docs/plans/phase-2/master.md
> and extends through docs/PLAN.md phase 3. Status snapshot embedded so the
> plan is self-contained.

## Status snapshot (what already landed)

- Harness (phase 1): complete — daemonclient, isolate, runner, checkers, scorecard
- Phase 2: regression gate (9 tasks, live), `diff` command, pre-push hook gate
  (user chose local hook over GitHub Actions), LongMemEval adapter + real-dataset
  cycle PROVEN (fetch→emit→validate→run), `--ignore-tags`, observability
  (classification_method in dispatch log/trace/transcripts), capability matcher
  default-off, ambient-stopword fix, prompt-router encoder classifier live,
  directive-parser prose false-positive fix
- Local models: 8080 LFM2.5-8B Q4_K_M (lazy agent work), 8081 LFM2.5-1.2B Q8
  (summarizer/aux), 8082 Prompt-Router 350M encoder (classifier)
- Known-failure: memory-recall-marker (recall→chat routing is semantically right;
  closes only with fine-tuned classifier or chat-agent file_write capability)

## Open decisions awaiting operator (NOT agent-actionable)

- GAIA: requires operator HF account + gate acceptance; HF_TOKEN export
- Any paid API judge models (default: local judge or zai)
- Publishing: no scorecard leaves this machine without explicit operator OK

## PHASE 2 REMAINDER (P2)

### P2.1 — LongMemEval full-suite run (leaf, group A)
- Files: suites/lmeval-data/* (gitignored), results/lmeval-full/* (gitignored)
- Emit FULL S-split (all 439 questions via existing adapter Limit=0), run
  in background batches of 25 with --auto-approved, scorecard per batch +
  aggregate
- Acceptance: aggregate accuracy vs the paper's GPT-4o-chat baseline range;
  per-question transcripts archived; scorecard labeled self-run
- Effort: mostly wall-clock (25 tasks × ~2-5 min ≈ 3-8 h background)

### P2.2 — Judge hardening for memory evals (leaf, group A, parallel with P2.1)
- Files: internal/checkers/llm_judge.go + tests; docs/RUNBOOK.md judge section
- Deterministic judge: pin local zai-free model or llama-server 8080 via
  --judge-cmd wrapper script; temperature 0 via prompt; rubric lint (reject
  rubrics that leak expected answers); double-judge agreement check (run
  judge twice, flag disagreement > 0.2)
- Acceptance: same answer set scored twice → identical scores (deterministic);
  unit tests for rubric lint

### P2.3 — Deterministic tool variants (leaf, group B; the ONE cross-repo leaf)
- meept side (small): config surface for per-conversation tool override mode
  `cached_fetch` (serve from local cache dir; miss → fail with explicit
  "cache-miss" error, no live network)
- bench side: internal/toolsdir/ — fixture cache populated by
  `meept-bench capture --url ...` (records fetch outputs to gitignored
  fixtures/ + SHA in manifest); suites reference `tools: {fetch: cached_fetch}`
- Acceptance: a suite task with cached fetch runs with networking disabled
  (daemon env HTTP_PROXY=dead-endpoint) and passes identically twice
- Effort: 1-2 days agent work

### P2.4 — Multi-turn steering probe (leaf, group B)
- Files: internal/daemonclient/steer.go + suites/steering.json (3 tasks:
  mid-run correction, follow-up question, cancel)
- Runner extension: optional `Turns []struct{DelayS int; Message string}` in
  Task schema; runner sends follow-up via daemon steering RPC while agent
  works; transcript records both prompts + combined trace
- Acceptance: steering probe suite green; closes GAPS.md gap 5
- Effort: 1 day

### P2.5 — Phase-2 completion docs (leaf, group C, last)
- Update docs/PLAN.md phase-2 status, README status section, RUNBOOK judge +
  cached-fetch sections; plan tree review per meept convention
- Acceptance: docs code-accurate (file:line verified), plan tree reviewed

## PHASE 3 (P3) — external benchmarks + published evidence

### P3.0 — GAIA access (OPERATOR, not agent)
- Accept gate at hf.co/datasets/gaia-bench/GAIA; export HF_TOKEN
- Decides: validation-split self-report vs test leaderboard submission
  (default: validation + self-run label)

### P3.1 — GAIA adapter (leaf, group D; needs P3.0 token)
- internal/suites/gaia/: fetch validation split by revision hash (never
  commit task content — BENCHMARKS.md rule 2), filter text-only L1 (metadata,
  no images/PDFs) ≈ 30-40 tasks; emit manifest: file-must-exist checkers
  where GAIA provides exact answers, llm_judge elsewhere
- GAIA artifacts = per-task results + aggregate accuracy + cost/task +
  transcripts; publishable per rule 2 while dataset stays gated
- Acceptance: 3-task probe green; then full text-only L1 run

### P3.2 — Context-policy measurement (leaf, group E; independent)
- WHAT: measure how meept's context assembly (memory tiers, FTS, session
  history, truncation policy) affects task success vs context budget
- HOW: bench-side harness that varies context knobs via daemon config
  (memory on/off, FTS on/off, history depth 0/5/20, epistemic tier on/off),
  runs the SAME LongMemEval + steering suites under each policy, emits a
  policy×accuracy×cost matrix (scorecard extension: --policy-label)
- Acceptance: matrix generated from 2×2 minimal policy grid on
  longmemeval-s subset (50 q); findings doc in results/ (gitignored) with
  headline numbers into docs/
- Effort: 2 days; reuses P2.1 batches

### P3.3 — Memory eval augmentation (leaf, group E; parallel with P3.2)
- Add LoCoMo as second memory suite (HF, research-permissive — reverify
  license at integration): conversation-grounded multi-hop QA, adapter
  mirrors lmeval (fetch by revision → emit → llm_judge/file_contains)
- Plus 5 synthetic episodic tasks in suites/fragments/memory-episodic.json
  (cross-session fact chains exercising meept's tiered memory directly)
- Acceptance: LoCoMo small-split probe green; episodic tasks green or
  documented known-failures
- Effort: 1-2 days

### P3.4 — Scorecard publishing pipeline (leaf, group F; LAST, needs P3.1-P3.3)
- `meept-bench publish --out docs/benchmarks/<suite>-<date>.md`: renders
  scorecard → markdown with mandatory self-run preamble (exact task list,
  seeds, models, date, HF revisions, auto-approval disclosure) per
  BENCHMARKS.md rule 3; commits ONLY derived artifacts, never task content
- Acceptance: one published GAIA-L1 scorecard + one LongMemEval scorecard,
  operator-reviewed before any external posting
- Effort: 0.5 day

## Execution order & parallelism

- Group A (P2.1 background run + P2.2 judge): parallel, start immediately
- Group B (P2.3 deterministic tools + P2.4 steering): parallel, start immediately
- Group C (P2.5 docs): after A+B land
- Group D (P3.1 GAIA): blocked on OPERATOR (P3.0 token)
- Group E (P3.2 context-policy + P3.3 memory augmentation): after P2.1 (needs
  stable LongMemEval runs as the measurement instrument)
- Group F (P3.4 publish): last

## Rules (carried forward)

- No commits by leaf agents; orchestrator reviews + commits
- Generated suites + results stay gitignored; adapters + templates committed
- Every scorecard: self-run label; never compare vs published numbers without
  comparable configs (PLAN.md non-goals)
- Pre-registered expectations before first runs (GAPS.md convention) — no
  rationalizing after the fact
