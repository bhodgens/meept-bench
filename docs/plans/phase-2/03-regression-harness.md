# Regression Suite: Cost, Trace & Harness-Integrity Tasks - Implementation Leaf

> **For the implementing agent:** You are the implementer for this leaf.
> Implement ALL tasks below. Do NOT commit — the orchestrator handles all
> git operations after review.

## Meta

- **Parent:** ../master.md
- **Scope:** Author regression tasks asserting harness integrity (tool trace
  arrives, cost is attributed, bus subscription survives) — the bugs fixed
  in commits a20b105c (meept) and 01c4f38 (bench).
- **Dependencies:** none
- **Estimated Context:** 40K
- **Concurrency Group:** A

## Goal

The harness itself can regress: bus subscriptions can die again (all traces
silently empty), cost attribution can drift back to cumulative totals. These
tasks fail loudly when the measurement infrastructure breaks — a benchmark
that cannot measure itself is worthless.

## Context

- `results/<suite>/results.jsonl` rows carry: verdict, cost_usd, tokens_in,
  wall_seconds, checks[] (with detail strings). Transcripts:
  `results/<suite>/transcripts/<suite>-<task>-a<N>.json` with tool_trace[].
- The pending-write detector annotates failing rows with a synthetic
  `pending_writes` check when the agent staged writes (01c4f38).
- The bus-subscription bug (a20b105c, meept internal/rpc/proxy.go) made
  tool_trace always empty. Detection: transcript.tool_trace length > 0 after
  any task that used tools.
- exit_zero checkers run in the task worktree with the worktree as cwd.

## Tasks

All tasks go in one fragment file `suites/fragments/harness-integrity.json`.

### Task 1: bus-trace-arrives

A task that forces tool use, plus an exit_zero checker that reads the
transcript the runner just wrote and asserts tool events exist.

```json
{
  "id": "bus-trace-arrives",
  "prompt": "Use the list_directory tool on the current directory, then write the word traced to a file named trace-check.txt in the repository root. Use file_write with direct:true.",
  "timeout_seconds": 300,
  "seeds": [1],
  "expect_agent": "coder",
  "tags": ["regression", "harness-integrity", "a20b105c"],
  "checkers": [
    {"type": "file_contains", "files": ["trace-check.txt"], "pattern": "traced"},
    {"type": "exit_zero", "command": ["sh", "-c",
      "python3 -c \"import json,glob; t=json.load(open(sorted(glob.glob('results/transcripts/*bus-trace-arrives*.json'))[-1])); import sys; sys.exit(0 if len(t.get('tool_trace',[]))>0 else 1)\" || (echo 'tool_trace empty - bus subscription regressed (a20b105c)'; exit 1)"]}
  ]
}
```

IMPORTANT path caveat: exit_zero commands run with the task WORKTREE as cwd,
but transcripts are written under the meept-bench repo's results/ dir. Adjust
the glob path prefix in Task 1's command to the absolute results dir
(`/Users/caimlas/git/meept-bench/results/transcripts/`) after confirming the
actual layout — check internal/runner/runner.go writeTranscript (outDirOr
defaults relative to the bench binary's cwd, which in CI/local runs is the
repo root). Verify empirically during live verification and use the path
that works; add a comment in the fragment file explaining the choice.

### Task 2: cost-attributed

Asserts per-row cost attribution works (delta, not cumulative garbage). This
task does not need the agent to do anything special — the CHECKER runs on
the results row. Because checkers cannot read results.jsonl rows for the
CURRENT run reliably (row written after checkers run), implement as a
meta-check: an exit_zero command that parses the PREVIOUS run's results.jsonl
if present and is a no-op pass otherwise — plus rely on 04-diff-command.md
to make cost regression visible. Task here is deliberately weak:

```json
{
  "id": "harness-cost-row-present",
  "prompt": "Write the word costed to a file named cost-check.txt in the repository root. Use file_write with direct:true.",
  "timeout_seconds": 300,
  "seeds": [1],
  "expect_agent": "coder",
  "tags": ["regression", "harness-integrity", "01c4f38"],
  "checkers": [
    {"type": "file_contains", "files": ["cost-check.txt"], "pattern": "costed"},
    {"type": "exit_zero", "command": ["sh", "-c",
      "test -f /Users/caimlas/git/meept-bench/results/regression/results.jsonl && python3 -c \"import json,sys; rows=[json.loads(l) for l in open('/Users/caimlas/git/meept-bench/results/regression/results.jsonl')]; bad=[r for r in rows if r.get('verdict')=='pass' and r.get('tokens_in',0)<0]; sys.exit(1 if bad else 0)\" || exit 0"]}
  ]
}
```

The real cost-regression gate lands in 04-diff-command.md (wall/cost deltas
per row). This task pins the cheap invariant only: tokens_in is never
negative on passing rows.

### Task 3: transcript-completeness

The transcript must carry prompt + final_reply for every task (this caught
silent-empty-reply bugs during bring-up):

```json
{
  "id": "transcript-completeness",
  "prompt": "Write the word complete to a file named transcript-check.txt in the repository root. Use file_write with direct:true.",
  "timeout_seconds": 300,
  "seeds": [1],
  "expect_agent": "coder",
  "tags": ["regression", "harness-integrity"],
  "checkers": [
    {"type": "file_contains", "files": ["transcript-check.txt"], "pattern": "complete"},
    {"type": "exit_zero", "command": ["sh", "-c",
      "python3 -c \"import json,glob; f=sorted(glob.glob('/Users/caimlas/git/meept-bench/results/transcripts/*transcript-completeness*.json'))[-1]; t=json.load(open(f)); import sys; sys.exit(0 if t.get('final_reply') and t.get('prompt') else 1)\""]}
  ]
}
```

## Verification

1. Build the bench binary. Run the fragment tasks via a temp manifest with
   `--keep-failed` and `--out results/regression-frag` (so the transcript
   globs are predictable during your verification).
2. All tasks PASS. For task 1, additionally hand-verify the transcript file
   it globs actually has >0 tool_trace entries (proves the checker isn't
   vacuously passing via the fallback `|| exit 0`).
3. Negative-test task 1 once: temporarily point its glob at a
   nonexistent path and confirm the checker FAILS, then restore. This proves
   the assertion has teeth.
4. Max 3 iterations; report blockers.

## Report

Report: tasks authored, the resolved transcript-path approach, negative-test
result, pass/fail per task, deviations.
