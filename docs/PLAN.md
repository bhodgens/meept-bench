# meept-bench Implementation Plan

Created 2026-08-24 from the agent-parity audit (meept repo: `docs/research/2026-08-24-agent-parity-audit.md`, section 10).

## Phase 1 — Harness (est. 2–4 weeks part-time)

1. **Daemon driver client** (`internal/daemonclient/`)
   - Go client speaking JSON-RPC over meept's Unix socket + WS for events.
   - Operations needed: start chat session, send message, subscribe to `chat_message` / `tool.execution.progress` / task lifecycle bus events, poll task state, read token/cost from analytics API.
   - Verify against a live daemon before writing any suite code.
2. **Task isolation** (`internal/isolate/`)
   - Fresh git worktree per attempt under a scratch root; workspace env var handed to daemon via project binding (`meept projects add`).
   - Teardown on success/failure; preserve-on-failure flag for postmortems.
3. **Runner core** (`cmd/meept-bench/`)
   - Input: suite manifest (JSON) listing tasks, each with prompt, checker type, max turns, timeout, seeds.
   - Per-task: spin worktree → ensure daemon healthy → send prompt → collect transcript + tool trace → run checker → record result row (JSONL) with tokens, USD cost, wall time, seed, verdict.
   - Flags: `--suite`, `--task` filter, `--rerun-failures`, `--attempts N`, `--model <alias>` (exercises meept's model reassignment), `--keep-failed`.
4. **Checkers** (`internal/checkers/`)
   - `exact_file`: file exists + content hash matches expected.
   - `file_contains`: regex/string present in named file(s).
   - `exit_zero`: recorded shell command exits 0 in the task worktree.
   - `llm_judge`: rubric-prompted judge model scores the final answer (for open-ended GAIA items); judges run at temperature 0, judged blind to model identity.
5. **Scorecard generator** (`cmd/scorecard/`)
   - Consumes result JSONL → markdown + JSON scorecard: pass rate, mean cost/task, mean turns, failure taxonomy (meept's own error kinds), per-seed variance.

## Phase 2 — Suites (order matters: cheapest signal first)

| Order | Suite | Why this order | Est. effort |
|-------|-------|----------------|-------------|
| 1 | LongMemEval-S (small split) first | no tool use, pure memory — isolates meept's biggest claimed differentiator fastest | 3–5 days |
| 2 | GAIA validation text-only L1/L2 subset | gated dataset — see BENCHMARKS.md rules; artifacts publishable, dataset is not | 1 week |
| 3 | Terminal-Bench subset | exercises runtime backends + validation gates | 1–2 weeks |
| 4 | τ-bench retail | policy compliance maps onto security engine/approvals | 2 weeks |
| 5 | SWE-bench Lite (50-task subset) | heaviest; needs SWE harness integration (Docker images) | 2–3 weeks |

Suite licensing/access notes in BENCHMARKS.md. Every suite gets an adapter translating its native format into our manifest.

## Phase 3 — Gap closure driven by results

Expected findings (pre-registered so we can't rationalize later):
- GAIA L1 without browser should be reachable; L2/L3 will stall on fetch/browser gaps.
- τ-bench will stress approval-gate ergonomics (agent-resolved approvals may need user-facing surfaces).
- LongMemEval will directly measure whether epistemic memory beats baseline FTS — if it doesn't, that's a real product finding, not a benchmark problem.

## Explicit non-goals

- No training on benchmark tasks, no prompt-tuning-to-the-test.
- No publishing until a full suite completes end-to-end twice with stable checkers.
- No comparison claims vs other agents' published numbers unless run configs are comparable; label everything self-run.

## Phase 2 status (2026-09-01)

Status as of the phase-2 wave; claims limited to artifacts present in the tree at commit time.

**Landed:**

- **Regression gate** (order 0, unplanned in the original table — promoted after the live smoke run exposed fixable daemon bugs): `suites/regression.json` — 9 tasks, one per fixed meept bug, each tagged with its origin commit/gap (`4f48e129`, `a0939721`, `a20b105c`, `01c4f38`, gap-12 session binding). Green twice consecutively on the 8 non-known-failure tasks in two local runs (`results/regression-run1`, `-run2`; run artifacts are gitignored and not committed — reproduce via docs/RUNBOOK.md). `memory-recall-marker` is tagged `known-failure`: meept's cross-conversation recall gap keeps it red by design.
- **Smoke suite**: `suites/smoke.json` (2 tasks), run per push in CI.
- **Scorecard diff**: `meept-bench diff --baseline FILE --current FILE` with best-attempt comparison; exit 1 on regression (`internal/diff/`).
- **CI workflow**: `.github/workflows/bench.yml` — smoke job on every push to main; nightly job (cron 07:00 UTC) runs smoke + regression and diff-gates against the `bench-baselines` branch baseline; only green runs update the baseline (`scripts/update-baseline.sh` green gate).
- **Docs**: `docs/RUNBOOK.md` — operator runbook for the gate.

**In flight (not landed):**

- LongMemEval-S template + adapter (order 1 in the table above).

**Not started:**

- GAIA (order 2), Terminal-Bench (order 3), τ-bench retail (order 4), SWE-bench Lite (order 5) — per the phase-2 table above; GAIA remains blocked per docs/BENCHMARKS.md gating rules.

Original phase descriptions above are kept unedited; this section only records what shipped.
