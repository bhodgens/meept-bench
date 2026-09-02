# Regression Suite: Routing & Dispatch Tasks - Implementation Leaf

> **For the implementing agent:** You are the implementer for this leaf.
> Implement ALL tasks below. Do NOT commit — the orchestrator handles all
> git operations after review.

## Meta

- **Parent:** ../master.md
- **Scope:** Author regression task entries (JSON) for meept dispatch/routing
  bugs; verify each passes against a live meept daemon.
- **Dependencies:** none
- **Estimated Context:** 40K
- **Concurrency Group:** A

## Goal

Encode meept's fixed routing bugs as permanent benchmark tasks so any
dispatcher regression fails the suite. These bugs were real: "create a file"
routed to chat (gap 9, fixed 4f48e129), `tool_hint=coder` fell through to
chat (fixed a0939721).

## Context

- Suite schema: `internal/suite/suite.go` — Task{ID, Prompt, Checkers,
  TimeoutS, Seeds, Tags, ExpectAgent}. Check types: `exact_file`,
  `file_contains` (fields: files, pattern), `exit_zero` (command),
  `llm_judge` (rubric). `expect_agent` asserts dispatch routing.
- Reference suite: `suites/smoke.json` — copy its style exactly.
- The runner creates one worktree per attempt; agents operate inside it.
- Requires a running meept-daemon on the default socket to verify
  (`/tmp/meept-bench-bin run --suite <file>` after building).

## Tasks

### Task 1: file-write-routes-coder task

Write a single-task suite fragment `suites/fragments/routing-file-write.json`
(valid Manifest JSON with one task):

```json
{
  "id": "file-write-routes-coder",
  "prompt": "Create a file named routing-check.txt in the repository root containing the text: dispatch works. Use file_write with direct:true.",
  "timeout_seconds": 300,
  "seeds": [1],
  "expect_agent": "coder",
  "tags": ["regression", "gap-9-routing", "4f48e129"],
  "checkers": [
    {"type": "file_contains", "files": ["routing-check.txt"], "pattern": "dispatch works"},
    {"type": "exit_zero", "command": ["test", "-s", "routing-check.txt"]}
  ]
}
```

### Task 2: tool-hint-coder task

Same file, second task. This one exercises the PLANNER path (the tool_hint
mechanism): prompt phrased so the dispatcher plans, and the step's tool_hint
resolves to coder (fixed in a0939721). The observable contract: task
completes AND the worktree file exists.

```json
{
  "id": "tool-hint-coder-maps-coder",
  "prompt": "Write code-style output to a file named hint-check.txt in the repository root: the single line hint ok. Use file_write with direct:true.",
  "timeout_seconds": 300,
  "seeds": [1],
  "expect_agent": "coder",
  "tags": ["regression", "a0939721", "tool-hint"],
  "checkers": [
    {"type": "file_contains", "files": ["hint-check.txt"], "pattern": "^hint ok$"}
  ]
}
```

Note: this task is only as strong as the daemon's planner emitting
tool_hint=coder for code intents. If live verification shows the routed agent
is coder but via a different path, keep the task — it still pins routing.

### Task 3: pending-write-direct (inverted assertion)

Third task in the same fragment. This one verifies the AGENT behaves
correctly (writes directly, not staged) — the failure mode the harness now
detects. A plain file check IS the assertion: staged writes never reach disk.

```json
{
  "id": "pending-write-direct",
  "prompt": "Create a file named direct-check.txt in the repository root containing: written directly. Use file_write with direct:true.",
  "timeout_seconds": 300,
  "seeds": [1],
  "expect_agent": "coder",
  "tags": ["regression", "pending-writes", "01c4f38"],
  "checkers": [
    {"type": "file_contains", "files": ["direct-check.txt"], "pattern": "written directly"}
  ]
}
```

## Verification (all tasks)

1. `cd /Users/caimlas/git/meept-bench && go build -o /tmp/meept-bench-bin ./cmd/meept-bench`
2. Combine fragments into a temp suite (wrap the tasks array in
   `{"suite":"regression-frag","tasks":[ ... ]}`) or add them to a copy of
   suites/smoke.json. Run:
   `/tmp/meept-bench-bin run --suite <tmp-suite> --keep-failed`
3. All tasks must PASS. If a task fails, inspect the transcript under
   `results/<suite>/transcripts/` and diagnose: routing mismatch (check
   daemon log `Dispatched request agent=` line), checker pattern mismatch
   (read the actual file in the kept worktree), or daemon not running.
4. Do not iterate more than 3 attempts per task; report blockers instead.

## Report

Report: tasks authored, verification output per task, any deviations.
