# Meept Bench Operator Runbook

How to run the meept regression gate, read failures, and update baselines.

## Prerequisites

- **meept daemon running.** The bench talks to it over a Unix socket:
  default `/Users/caimlas/.meept/meept.sock`, override with
  `MEEPT_BENCH_SOCKET=/path/to/meept.sock`.
- **Provider key configured** in the daemon (it holds all model credentials).
- **Python 3 on PATH** — two harness-integrity tasks run `python3` one-liners
  in their exit_zero probes.
- **Build:**

  ```sh
  go build -o /tmp/meept-bench-bin ./cmd/meept-bench
  ```

- **Sanity check** (daemon + socket reachable):

  ```sh
  /tmp/meept-bench-bin doctor
  ```

## Commands

```sh
# Full regression gate (~10-15 min, 9 tasks x 300s timeouts):
/tmp/meept-bench-bin run --suite suites/regression.json --out results/regression-run1 --auto-approved

# Smoke check (2 tasks, ~2 min) before a full run:
/tmp/meept-bench-bin run --suite suites/smoke.json --out results/smoke --auto-approved

# Diff two runs (exit 0 = clean, exit 1 = regressed):
/tmp/meept-bench-bin diff --baseline results/regression-run1/results.jsonl \
                          --current  results/regression-run2/results.jsonl

# Keep failed-attempt worktrees for postmortems:
/tmp/meept-bench-bin run --suite suites/regression.json --out results/regression-dbg --keep-failed --auto-approved
```

## Where results land

| What | Where |
|------|-------|
| Result rows | `<out>/results.jsonl` — one JSON object per attempt, best attempt wins for scoring |
| Transcripts | `<out>/transcripts/<suite>-<task>-a<N>.json` — full prompt, tool_trace, final_reply |
| Kept worktrees | `~/.meept-bench/` scratch root (with `--keep-failed`, failed-attempt trees survive for inspection) |

## Failure triage

Symptom → what to check, in order. Every path has a concrete probe.

1. **Routing mismatch** (`expect_agent` row fails, e.g. `file-write-routes-coder`
   reports a chat/route mismatch):
   The dispatch trace is the ground truth. Grep the daemon log:
   ```sh
   grep 'Dispatched request agent=' ~/.meept/daemon.log
   ```
   If the dispatched agent is not `coder` for a file-write prompt, routing
   regressed (see gap-9 / 4f48e129 / a0939721 in task tags).

2. **Checker pattern miss** (task ran, verdict `fail`, checker says
   `file_contains` no match): inspect the kept worktree file itself — was the
   text written at all, or written with different wording?
   ```sh
   ls -t ~/.meept-bench/*/  # newest scratch worktrees
   find ~/.meept-bench -name '<checked-file>.txt' -newer /tmp/meept-bench-bin
   ```
   If the file content diverges from the pattern, decide: agent behavior
   drifted (fix agent) or pattern too strict (tighten/loosen checker + rerun
   green twice before committing the change).

3. **Empty tool_trace** (`bus-trace-arrives` fails with
   `tool_trace empty or transcript missing - bus subscription regressed (a20b105c)`):
   bus delivery regressed in meept. Check subscriber count:
   ```sh
   /tmp/meept-bench-bin doctor | grep bus_subscribers
   ```
   See meept commit a20b105c for the fix shape.

4. **Daemon unreachable** (`doctor` prints `FAIL daemon ping`):
   wrong/missing socket. Resolve the path and retry:
   ```sh
   ls -la /Users/caimlas/.meept/meept.sock /tmp/meept/meept.sock 2>/dev/null
   export MEEPT_BENCH_SOCKET=/tmp/meept/meept.sock   # if the daemon listens there
   /tmp/meept-bench-bin doctor
   ```

5. **Transcript missing for an exit_zero probe** (probe errors
   `transcript missing`): the probe globs
   `/Users/caimlas/git/meept-bench/results/**/transcripts/*<task-id>*.json`
   and takes the newest match — it resolves regardless of `--out`. If it
   still misses, transcripts aren't being written at all; check
   `internal/runner/runner.go writeTranscript` and the run's out dir.

6. **Known-failure went green** (`memory-recall-marker` passes): that is a
   *reportable change* — meept's dispatcher started injecting the marker
   memory (relevance threshold behavior changed). Run the full suite twice;
   if green twice, the gap closed upstream and the tag can be dropped.

## Baseline updates

After a **green full run** (all non-known-failure tasks pass, green twice
consecutively), refresh the local baseline convention:

```sh
cp results/regression-run2/results.jsonl results/baseline/regression.jsonl
mkdir -p results/baseline   # if it doesn't exist yet
```

Local convention until CI lands — the baseline is not committed yet. Diff
against it before merging changes that touch dispatch, memory, or the bus.

## Known-failures

| Task | Tag | Reason |
|------|-----|--------|
| `memory-recall-marker` | `known-failure` + `xfail: ...` encoded in tags | meept cross-conversation recall gap: daemon memory is global, but the dispatcher only auto-injects memories scoring > 0.3 relevance and the FTS scores this marker fact at ~0.2, so fresh-conversation recall silently drops it. Checker accepts either the codeword or an explicit `MEMORY-UNAVAILABLE` admission so the suite completes while the gap is open. Keep LAST in the task array. |

## Cost note

Rows show `$0.00` on free-tier providers; the cost delta columns in `diff`
still work — they compare the recorded values, so any nonzero-cost provider
change shows up as a delta regardless of absolute magnitudes.
