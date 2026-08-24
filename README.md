# meept-bench

Outcome-level benchmark harness for [meept](https://github.com/bhodgens/meept), the personal AI agent daemon. This repo exists because meept ships strong internal QA (custom static analyzers, generated connectivity graphs, race tests) but zero published evaluation evidence. Competitors publish GAIA artifacts, memory evals, and context-policy measurements; meept publishes nothing measurable. This fixes that.

## Status: scaffold

Phase 1 harness work has not started. See [docs/PLAN.md](docs/PLAN.md).

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
