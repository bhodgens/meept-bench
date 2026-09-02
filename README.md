# meept-bench

Outcome-level benchmark harness for [meept](https://github.com/bhodgens/meept), the personal AI agent daemon. This repo exists because meept ships strong internal QA (custom static analyzers, generated connectivity graphs, race tests) but zero published evaluation evidence. Competitors publish GAIA artifacts, memory evals, and context-policy measurements; meept publishes nothing measurable. This fixes that.

## Status: Phase 2 in progress — regression gate live

The runner drives a live meept daemon end to end: suite manifest → per-task git worktree → JSON-RPC chat over the unix socket → bus event trace → checkers → JSONL results → markdown/JSON scorecard. Phase 1 (harness) is complete; see [docs/PLAN.md](docs/PLAN.md) for phase status. What has landed in phase 2:

- **Regression gate live** — [suites/regression.json](suites/regression.json): 9 tasks, one per fixed meept bug, each tagged with its origin commit/gap (`4f48e129` routing, `a0939721` tool-hint, bus trace, session/workdir binding, cost rows). Green twice consecutively on the 8 non-known-failure tasks in local runs (`results/regression-run1`, `-run2`; run artifacts are gitignored, not committed — reproduce per [docs/RUNBOOK.md](docs/RUNBOOK.md)). `memory-recall-marker` stays red by design (`known-failure` tag): meept's cross-conversation recall gap.
- **Scorecard diff** — `meept-bench diff --baseline --current`: best-attempt comparison across two runs, exit 1 on regression ([internal/diff](internal/diff)).
- **CI** — [.github/workflows/bench.yml](.github/workflows/bench.yml): smoke suite on every push to main; nightly regression run + diff gate against the `bench-baselines` branch (only green runs update the baseline).
- **Landed** — LongMemEval-S template + adapter (MIT license-cleared; real-dataset fetch pending operator-approved bulk download; template e2e pending meept classifier recovery).

## Quick start

```
go build ./...                      # stdlib-only, no external deps
./meept-bench doctor                # verify the daemon is reachable
./meept-bench run --suite suites/smoke.json --attempts 1
./meept-bench run --suite suites/regression.json --attempts 1 --auto-approved   # the gate: 9 tasks
./meept-bench diff --baseline results/regression-run1/results.jsonl --current results/regression-run2/results.jsonl
./meept-bench scorecard results/smoke/results.jsonl
```

Requirements:

- A running `meept-daemon` (JSON-RPC on its default socket; override with `MEEPT_BENCH_SOCKET`).
- The repo under test is a git repository — every attempt gets a fresh `bench/<task>` worktree under `--scratch` (default `/tmp/meept-bench`).
- For `llm_judge` checkers, an external judge command via `--judge-cmd "prog args..."`. It reads `<rubric>\n---\n<answer>` on stdin and prints `<score 0..1> <rationale>`. Judges run blind to model identity.

### Run flags

| Flag | Meaning |
|------|---------|
| `--suite FILE` | task manifest (required) |
| `--task SUBSTR` | run only tasks whose ID contains SUBSTR |
| `--attempts N` | attempts per task |
| `--model ALIAS` | override model (exercises meept model reassignment) |
| `--keep-failed` | preserve failed-attempt worktrees for postmortems |
| `--auto-approved` | disclose approval gates ran with no human present |

Outputs land in `results/<suite>/`: `results.jsonl` (one row per attempt: verdict, seed, cost, wall time, HF revision), `transcripts/*.json` (prompt, final reply, distilled tool trace from `tool.execution.progress` / `chat_message` bus events), and `scorecard.{md,json}` (pass rate, mean cost/task, failure taxonomy by meept error kind, per-seed variance). Every scorecard is labeled **self-run** per docs/BENCHMARKS.md rule 3.

## Operating the gate

Prerequisites, run commands, failure triage, and baseline policy: [docs/RUNBOOK.md](docs/RUNBOOK.md).

## What runs here

A headless runner drives a meept daemon over its RPC/WS interface:

```
suite.json (task manifest)
    ↓
meept-bench runner (Go, this repo)
    ↓  RPC: chat requests, tool-event subscription, result capture
meept-daemon (fresh worktree per task)
    ↓
results/<suite>/<task>/<attempt>/  (transcript, tool trace, verdict, cost)
    ↓
scorecard generation (markdown + JSON)
```

Design rules inherited from the 2026-08 parity audit:

- Drive the daemon API directly. No screen-scraping, no TUI automation.
- Fresh git worktree per task. Tasks cannot see each other's state.
- Deterministic seeds where the model supports them; record every seed either way.
- Rerun-failures-only mode for expensive suites.
- Self-run results are labeled as self-run. Failures publish alongside wins.
- Cost per task is recorded (tokens + USD from meept's own budget tracker).

## Benchmark ladder

Chosen so each rung exercises a meept subsystem and matches what competitors already publish:

| Rung | Suite | Measures | Meept subsystem exercised |
|------|-------|----------|---------------------------|
| 1 | GAIA validation, text-only L1/L2 subset | general assistant competence | loop, steering, memory recall |
| 2 | Terminal-Bench | terminal/tool operations | shell tool, runtime backends, validation gates |
| 3 | τ-bench (retail/airline) | tool-use policy compliance | security engine, approval gates, taint sinks |
| 4 | LongMemEval / LoCoMo | long-term memory accuracy | episodic FTS5, knowledge graph, epistemic claims |
| 5 | SWE-bench Lite | real coding | coder specialist, worktrees, auto-lint reflection |
| deferred | GAIA L3, WebArena, OSWorld | browser/computer-use | blocked on computer-use tooling |

Suite access and licensing notes: [docs/BENCHMARKS.md](docs/BENCHMARKS.md).
Gaps these benchmarks will expose before scores are competitive: [docs/GAPS.md](docs/GAPS.md).

## License

MIT. See [LICENSE](LICENSE).
