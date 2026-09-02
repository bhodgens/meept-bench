# Regression Suite: Memory & Session Tasks - Implementation Leaf

> **For the implementing agent:** You are the implementer for this leaf.
> Implement ALL tasks below. Do NOT commit — the orchestrator handles all
> git operations after review.

## Meta

- **Parent:** ../master.md
- **Scope:** Author regression tasks exercising meept memory and session
  identity (GAPS.md gap 12); verify against a live daemon.
- **Dependencies:** none
- **Estimated Context:** 40K
- **Concurrency Group:** A

## Goal

meept's claimed differentiator is memory. There is no permanent task proving
basic recall works. Session identity is split (session-… vs conv-…, gap 12) —
a recall task through the same path the harness uses pins that contract.

## Context

- The harness sends prompts via `chat` RPC with a fresh conversation per
  task. Memory recall must work across conversations for this task to be
  meaningful: task 1 stores a fact, task 2 (separate conversation) recalls
  it. Within one suite run, tasks share the daemon but not conversations.
- The daemon's `memory.recent` / `search.semantic` RPCs exist; agents also
  have memory tools. Prefer driving through agent prompts (what we benchmark)
  not raw RPCs.
- Checker tools available: file_contains, exit_zero, llm_judge (needs judge
  command; avoid — use file evidence).

## Tasks

### Task 1+2 pair: memory store then recall

Suite fragment `suites/fragments/memory-recall.json` with TWO tasks. The
second task asserts recall of what the first stored. Marker string must be
unique per run to avoid false positives from stale daemon memory — use a
fixed nonce for now (deterministic suites want fixed markers; note this in
tags):

```json
[
  {
    "id": "memory-store-marker",
    "prompt": "Remember this exactly: the deployment codeword is BENCH-7391. Store it in memory so it survives across conversations. Confirm by writing the word stored to a file named mem-store.txt in the repository root. Use file_write with direct:true.",
    "timeout_seconds": 300,
    "seeds": [1],
    "expect_agent": "chat",
    "tags": ["regression", "memory", "gap-12-session"],
    "checkers": [
      {"type": "file_contains", "files": ["mem-store.txt"], "pattern": "stored"}
    ]
  },
  {
    "id": "memory-recall-marker",
    "prompt": "What is the deployment codeword I asked you to remember earlier? It starts with BENCH-. Write just the codeword to a file named mem-recall.txt in the repository root. Use file_write with direct:true.",
    "timeout_seconds": 300,
    "seeds": [1],
    "expect_agent": "chat",
    "tags": ["regression", "memory", "gap-12-session"],
    "checkers": [
      {"type": "file_contains", "files": ["mem-recall.txt"], "pattern": "BENCH-7391"}
    ]
  }
]
```

IMPORTANT ordering caveat: suite tasks run in order within one run, each in
its own conversation. If the daemon's memory is conversation-scoped only,
task 2 will fail — that is a REAL FINDING about meept memory semantics, not a
broken task. If it fails for that reason, keep the task but add
`"xfail": true`-style documentation: report the finding, mark the task
`{"tags": [..., "known-failure"]}` and check with a pattern matching either
the codeword OR an explicit "I don't have access" reply file so the suite
still completes. Escalate the finding in your report.

### Task 3: session-binding sanity

Third fragment task proving the harness's own binding contract (gap 12): the
agent can see its working directory is the fresh worktree.

```json
{
  "id": "session-workdir-bound",
  "prompt": "Write the absolute path of your current working directory to a file named cwd.txt in the repository root (one line, just the path). Use file_write with direct:true.",
  "timeout_seconds": 300,
  "seeds": [1],
  "expect_agent": "coder",
  "tags": ["regression", "gap-12-session", "01c4f38"],
  "checkers": [
    {"type": "file_contains", "files": ["cwd.txt"], "pattern": "/tmp/meept-bench/"},
    {"type": "exit_zero", "command": ["sh", "-c", "! grep -q '/Users/caimlas/git/meept-bench$' cwd.txt"]}
  ]
}
```

The second checker is the regression teeth: the cwd must be the worktree
under the scratch root, NOT the meept-bench repo itself.

## Verification

1. Build: `go build -o /tmp/meept-bench-bin ./cmd/meept-bench` (in
   /Users/caimlas/git/meept-bench).
2. Wrap tasks in a temp manifest, run with `--keep-failed`, all must PASS
   (or the documented known-failure shape for the memory pair).
3. Inspect kept worktrees to confirm checker patterns matched real content.
4. Max 3 iterations per task; report blockers.

## Report

Report: tasks authored, pass/fail per task, the memory-scope finding if task
2 fails, any deviations.
