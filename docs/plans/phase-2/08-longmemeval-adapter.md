# LongMemEval-S Adapter - Implementation Leaf

> **For the implementing agent:** You are the implementer for this leaf.
> Implement ALL tasks below using TDD. Do NOT commit — the orchestrator
> handles all git operations after review.

## Meta

- **Parent:** ../master.md
- **Scope:** Adapter fetching LongMemEval-S from HuggingFace at run time and
  emitting a task manifest the existing runner consumes; contract 3 in
  ../master.md.
- **Dependencies:** none hard; ideally lands after 04 (diff makes results
  meaningful). Checkers are stable, so authoring can proceed in parallel.
- **Estimated Context:** 80K
- **Concurrency Group:** C

## Goal

The first REAL benchmark (docs/PLAN.md phase 2, order 1): LongMemEval-S —
no tool use, pure memory recall over long conversation histories. Directly
measures meept's claimed differentiator. License check first (BENCHMARKS.md
rule 1).

## Context

- Dataset: `xiaowu0162/LongMemEval` on HuggingFace (VERIFY the exact dataset
  id and its license at integration time — BENCHMARKS.md rule 1 requires
  re-verification; record the license and the dataset revision hash in the
  adapter config and output). Split "S" (small). Each item: a question, a
  haystack of sessions, an answer.
- How meept agents answer: the agent must find the answer in ITS memory. Two
  viable harness paths:
  (a) HAYSTACK-AS-MEMORY: adapter stuffs the haystack into the daemon
      memory (via agent prompts or memory RPC) in task A; task B asks the
      question and checks the answer. Expensive (S still has ~500 items);
      template suite uses 5 items.
  (b) HAYSTACK-AS-CONTEXT: adapter writes the haystack to worktree files;
      prompt says "answer using the session logs in this directory".
      Cheaper, tests synthesis not memory — acceptable for template
      validation, and closer to (a) later.
  The adapter supports BOTH modes via config; template ships mode (b).
- Licensing/access rules: BENCHMARKS.md — never commit task content;
  fetch by revision hash under the user's HF token (env HF_TOKEN);
  generated manifests are gitignored; record revision in every emitted task
  (manifest `meta` map or task tags).
- Row/manifest schema: internal/suite/suite.go. llm_judge needs a judge
  command at RUN time (--judge-cmd); the adapter just emits rubrics.

## Tasks

### Task 1: license + dataset verification doc

`docs/LONGMEMEVAL.md`: record the exact HF dataset id, verified license
(name + where stated), revision hash you validated against, split S shape
(item count, fields), and the access path. If the license forbids derived
manifests even when generated at runtime and not committed, STOP and report
— do not proceed.

### Task 2: adapter package (internal/suites/lmeval/, TDD)

```
type Config struct {
    DatasetID  string // e.g. "xiaowu0162/LongMemEval"
    Revision   string // pin for reproducibility
    Split      string // "S"
    Limit      int    // 0 = all; template uses 5
    Mode       string // "context" | "memory"
    Seed       int64  // deterministic item selection
}

// Fetch questions the HF datasets-server HTTP API (no SDK dependency;
// stdlib-only repo): GET https://datasets-server.huggingface.co/rows
// ?dataset=<id>&config=<config>&split=<split>&offset=0&length=<limit>
// (discover the correct config name from the /splits endpoint; handle the
// API's 10-row page limit by paging). Tests use httptest servers serving
// fixture JSON — NO network in unit tests.
func (c Config) Fetch(ctx context.Context, hc *http.Client) ([]Item, error)

// Emit converts items to suite.Task values. Mode context: haystack written
// by the RUNNER? No — the runner knows nothing of haysticks. Instead Emit
// produces tasks whose PROMPT includes an inline instruction to read
// sessions/haystack files the ADAPTER also emits into a seeded directory:
// the adapter writes per-task haystack files under
// suites/lmeval-data/<task-id>/ (gitignored) and the prompt references the
// absolute path. Deterministic task IDs: lmeval-s-<index>
// (sha-prefix of question for stability).
func (c Config) Emit(items []Item, dataDir string) (*suite.Manifest, string /*dataDir*/, error)
```

Checker per task: `file_contains` on an answer file with the gold answer as
pattern (exact-match QA items), or `llm_judge` rubric for abstruse items —
detect: if Item gold answer is short/exact → file_contains; else llm_judge
with rubric "Does the answer state <gold>? Accept paraphrase." (min_score
default 0.7).

Tests (no network): fixture JSON from the real schema shape (fetch one real
item ONCE during development, save redacted/trimmed as testdata); paging
logic; deterministic ID generation; Emit produces valid
suite.Manifest (call suite.Validate in the test); mode context prompt
contains the data dir path.

### Task 3: CLI wiring

`meept-bench lmeval --config suites/longmemeval.local.json --data-dir
suites/lmeval-data` writes `suites/longmemeval-s.generated.json` + data
files, prints the run command. Config file schema documented in
docs/LONGMEMEVAL.md. Both generated outputs gitignored (add to .gitignore:
`suites/longmemeval-s.generated.json`, `suites/lmeval-data/`).

### Task 4: template suite (committed)

`suites/longmemeval-s.template.json`: 5 tasks produced by running the
adapter in mode context with Limit 5, THEN hand-redact: replace haystack
content references with a small checked-in fixture (2-3 synthetic sessions
under `suites/lmeval-data-template/` — synthetic content you author, NOT
dataset content) so the template runs WITHOUT a HF token and validates the
whole pipeline end-to-end in CI. Template tasks must pass against a live
daemon with any competent model.

### Task 5: live verification

With HF_TOKEN set (user's token, from env), run the adapter for real (Limit
5, mode context), run the generated suite against the live daemon, and
report pass/fail per task. If HF access or dataset shape differs from
assumptions, STOP and report findings — do not fabricate.

## Report

Report: license verdict, files created, test outputs, template suite
e2e result, live verification result (per-task), deviations.
