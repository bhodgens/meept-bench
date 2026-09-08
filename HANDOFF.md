# meept-bench ↔ meept — Session Handoff (2026-09-06)

> Context: multi-session effort to (a) fix every bug the bench surfaces,
> (b) complete phase 2 of the benchmark plan, (c) prep phase 3 (GAIA,
> context-policy, published scorecards). All work committed unless noted.

## State at handoff

- **meept main**: healthy, all fixes committed (HEAD `d4731a4e`).
  Requires ONE daemon restart to pick up everything (currently running
  an older binary from 09-04).
- **meept-bench main**: Makefile + suites + fixes committed (HEAD
  `4cccefe`+). Generated LongMemEval suite on disk (500 tasks, pinned
  HF rev `2ec2a557`), gitignored.
- **Known sibling WIP (not ours, do not touch)**: `loop.go` RecordUsage
  signature churn, `config/mcp_servers.json5`, stale
  `TestConfigLoads` (providers_config_test.go asserts pre-`812a0555`
  model names — sibling changed config without updating its test),
  untracked `nudge_*_test.go` files.

## What landed this session (newest last)

| Commit | What |
|---|---|
| `d4731a4e` | Token accounting: `llm_calls` ledger (per provider/model/**agent**, sent/received/**cached**, error, latency) + `QueryLLMCallUsage` + model_performance finally populated. Cached tokens parsed on ALL paths (streaming + Codex added). |
| `94d71aa1` | Quota-aware job deferral: quota-class step failures DEFER to ResetAt (max 10 / 6h) instead of failing; observable `deferred_quota` event. Kills the lmeval "drown at 300s deadline" failure. |
| `63aa7748` | Thinking-safe summarizer: `llm.DisableThinking()` exported option + `<think>`/reasoning strip. 8B-A1B now safe as summarizer (thinking off + leakage-proof). |
| `8c4b1b1f` | Panic fix: `OnJobCompleted` nil-deref (tactical.go:786) + REAL root cause — task/queue stores used mattn-style DSN keys that modernc.org/sqlite silently ignores (no WAL, no busy_timeout ever). Now honored `_pragma=` form + MaxOpenConns(1). |
| `1fd8a5a6` | Researcher gains file_write (fetch+write tasks complete); planner prompts advertised research→analysis (starving the hint) — fixed; agentIDToToolHint missed researcher — added. |
| `9a4ed339` | Runtime adoption ownership: pidfiles now JSON {pid, token}; foreign-manager adoption = observed-not-owned (production sidecars can't be killed by test binaries' StopAll). CLI reads via exported ParsePIDFile. |
| `56d7c348`+`79b73b15` | GAPS.md: adoption race diagnosis, gate failed-closed proof, panic, capability gap. |

Earlier in session (also committed): whitespace-stop terminal fix,
silent-fake-success fix (RunOnce error propagation), alias rotation on
429 incl. streaming, in-use pre-warm gate, `--ignore-tags`, steering
suite (P2.4), judge hardening (local deterministic judge + double-judge +
rubric lint), Makefile, pre-push gate on local 8B, capability-matcher
default-off + ambient stopwords, prompt-router 350M encoder as
classifier (8082), directive-parser prose false-positive fix,
LongMemEval adapter numeric-answer coercion.

## Launching LongMemEval (HELDED for operator go)

Preconditions (all satisfied once daemon restarts):
1. `cd ~/git/meept && go build ./... && go install ./cmd/meept-daemon && ./bin/meept daemon restart`
2. Verify: all 3 runtimes healthy (8080 8B-Q4, 8081 1.2B-Q8, 8082 router)
3. `cd ~/git/meept-bench && make gate DEV_GATE=1` — must be green (validates local-8B chain)

Launch:
```
cd ~/git/meept-bench && make lmeval
```
- 500 tasks, single-turn (NO steering involvement), `file_contains` ×406
  + `llm_judge` ×94 (local 8B judge, quota-free).
- Agent work model: daemon default chain = **agnes-2.5-flash first**
  (operator instruction), locals as fallback. With quota-deferral now
  live, agnes 429 windows defer tasks (≤10 / 6h per job) instead of
  failing them — expect multi-hour-to-overnight wall.
- Local-only alternative: `make lmeval MODEL=local/lfm-8b-q4` (~6-9h,
  zero quota exposure). MODEL was deliberately left at daemon-default
  per operator instruction.
- Progress: `tail -f results/lmeval-full/../lmeval-full-run.log` or the
  results.jsonl row counter.

## Answered questions (for posterity)

- **agnes failures**: 429-only (code 1308, 5-hour rolling window,
  reset-at announced in body). No 401/403s since 08-31 one-off.
  Drains in ~90min at benchmark intensity; capacity est. low-millions
  of tokens/window (not disclosed by API).
- **z.ai**: zero rate-limit data (never reached under load).
- **Backoff**: RPM waits in-call; 5h quota parks requests (parks.db,
  10m poll, 24h max); deferral now moves TASKS onto the same clock.
- **1.2B vs 8B summarizer**: summarization = compression, not reasoning
  → instruct preferred; but 8B-A1B MoE ≈ 1B active params = same speed
  class, better multi-topic comprehension, and is now thinking-safe.
  1.2B remains summarizer; promoting 8B is a config-line A/B
  (compare memory-eval results).
- **Hermes**: not in the bench path (bench → daemon RPC direct). Its
  Agnes-AI provider entry says `agnes-2.0-flash` (meept uses 2.5) —
  parity bump is operator's call.

## Open items

1. **LongMemEval launch** — operator go (see above).
2. **GAIA phase 3** — operator: accept gate at
   `hf.co/datasets/gaia-bench/GAIA`, export HF_TOKEN. Then P3.1 adapter
   per docs/plans/phase-2-3/master.md.
3. **Sibling WIP to settle**: stale `TestConfigLoads` (812a0555 debt),
   loop.go RecordUsage churn, mcp_servers.json5.
4. **P2.5 docs pass** — update PLAN.md/README status; Makefile exists.
5. **New findings filed in GAPS.md (not yet fixed)**:
   - `TestConfigLoads` stale assertions (812a0555 debt)
   - LLM classifier labels fetch+write phrasing `intent=code`
     (self-corrects via planner retry; classifier-vocabulary item)
   - Planner spec drift (invented /tmp/example_fetch.json target) —
     intermittent; reviewer judged against invented spec
   - Runtime-manager unit tests churn LIVE daemon runtimes (shared
     ports/pidfiles) — tests need ephemeral ports/scratch dirs
6. **Stray dirs in meept-bench** — operator declined deletion 3×;
   still present: `cmd/probe-dispatch-tmp/`,
   `internal/suites/{longmemeval-s.template.json,lmeval-data-template/}`.
7. **Token accounting validation** — after daemon restart, run one
   task and confirm `llm_calls` rows land in metrics.db with agent
   attribution (the instrumentation is committed but never exercised
   against the live daemon).

## Key commands

```
# Gate (local 8B, quota-free):
cd ~/git/meept && git push            # hook runs gate automatically
# or manually: cd ~/git/meept-bench && make gate DEV_GATE=1

# LongMemEval:
cd ~/git/meept-bench && make lmeval   # agent work: daemon default (agnes-first)
make lmeval MODEL=local/lfm-8b-q4     # local-only variant

# Diff vs baseline:
meept-bench diff --baseline results/baseline/regression.jsonl \
                 --current results/lmeval-full/results.jsonl

# Token usage since a time (SQL on metrics.db):
sqlite3 ~/.meept/metrics.db "SELECT provider, agent_id, SUM(tokens_sent), SUM(tokens_received), SUM(tokens_cached) FROM llm_calls WHERE timestamp >= '2026-09-06' GROUP BY provider, agent_id;"
```
