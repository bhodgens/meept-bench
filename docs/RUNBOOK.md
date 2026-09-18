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

# Gate one-liner — run after ANY meept change (exit 0 = safe to merge):
/tmp/meept-bench-bin run --suite suites/regression.json --out results/regression --auto-approved && \
/tmp/meept-bench-bin diff --baseline results/baseline/regression.jsonl \
                          --current  results/regression/results.jsonl

# Keep failed-attempt worktrees for postmortems:
/tmp/meept-bench-bin run --suite suites/regression.json --out results/regression-dbg --keep-failed --auto-approved
```

## Where results land

| What | Where |
|------|-------|
| Result rows | `<out>/results.jsonl` — one JSON object per attempt, best attempt wins for scoring |
| Transcripts | `<out>/transcripts/<suite>-<task>-a<N>.json` — full prompt, tool_trace, final_reply |
| Kept worktrees | `~/.meept-bench/` scratch root (with `--keep-failed`, failed-attempt trees survive for inspection) |

## Routing evidence and assertions

Tasks may declare `expect_agent`, `expect_intent`, and
`forbidden_classification_methods` (an array, e.g. `["short_message_guard"]`).
Empty optional expectations preserve outcome-only grading. Blank method entries
and whitespace-only intents are rejected at load time; artifact checkers remain
required.

Rows and transcripts include `routing` and `routing_checks`. Each routing check
has `check`, `status` (`pass`, `fail`, or `error`), and optional `detail`.
`checks` continues to hold artifact/outcome checks separately. An observed
mismatch gives `verdict=fail`, `error_kind=routing_mismatch`; unavailable required
evidence gives `verdict=error`, `error_kind=routing_evidence`. Both retain outcome
checks for completed turns, plus the original prompt, reply, and tool trace.
Artifact success cannot overwrite a routing failure/error. Incomplete, failed,
and timeout turns do not earn artifact-check success. With no expectations,
unavailable routing does not change an outcome-only verdict.

`routing.evidence_status` is `available`, `partial` (one or more fields absent),
or `unavailable`. Partial evidence can satisfy only assertions whose required
fields exist. Missing `routing` in old rows/transcripts means **unknown**, not
verified. The legacy `routed_agent` and `classification_method` transcript fields
remain readable but alone do not prove association.

The supported association is `fresh_session_unique_dispatch`: the runner calls
`session.create` per attempt, requires both returned IDs, submits one primary
turn, and reads `session.dispatch_trace` once with `session_id` equal to that
conversation ID and `limit=2`. Exactly one non-errored, matching-session record
is required. Zero, multiple, wrong-session, malformed, or unavailable records
cannot pass requested assertions. Tasks with scheduled `turns` have unavailable
routing evidence even when only one record happens to be returned.

Producer contract inspected in meept: `internal/rpc/dispatch_trace.go:20-40`
returns `{entries, count}`; **count is the returned slice length, not a total**.
`internal/metrics/store.go:888-914` supplies `session_id`, `agent_id`,
`intent_type`, `classifier_method`, `task_id`, `turn_no`, `model`, and
`input_hash`, but no `turn_id` or `dispatch_id`. The observation maps `model`
to `classifier_model`: it is classifier provenance, not proof of the serving
agent model. `task_id`/`turn_no` are retained as diagnostics, not treated as
stable turn identity. Freshness is source-backed: the RPC proxies to
`session.Handler.handleCreate` (`session.go:1686-1703`), which calls store.Create;
SQLiteStore.Create (`store_sqlite.go:447-487`) generates new session/conversation
IDs and inserts a new session. No timestamp association or latest-entry fallback
is used. Multi-turn routing acceptance needs a producer-supplied stable
turn-to-dispatch association before it can be enabled.

## Routing repair suite (suites/routing-repair.json)

Nine frozen, synthetic, single-turn regression tasks (leaf 02 of the
2026-09-17 routing-acceptance plan). This is a repair-evidence suite, not an
unseen model-quality benchmark; it runs with a fresh `--out` per invocation
and never against the production scheduler.

- **Fixtures**: hash-pinned via `setup_files` per task. Sources resolve
  relative to the manifest under `suites/routing-repair-data/`, are copied
  into each attempt's fresh worktree before the daemon is contacted
  (`internal/suite/setup.go`), and must pass their lowercase `sha256` at
  attempt time — a mismatch aborts the task with verdict `error` before any
  chat submit. No ambient worktree sync, no untracked-file leakage, no
  overwrite of existing files, `.git`/parent/absolute destinations rejected
  at load time. Prompts carry no setup instructions (prompt shape is the
  diagnostic).
- **Checker integrity**: `trusted_python` loads manifest-relative, hash-pinned
  script bytes into harness memory at suite load. It never copies checker code
  into the agent worktree, and executes Python with `-I -B -c` to ignore
  worktree/PYTHONPATH imports. `setup_files` remain freely agent-editable.
  This is an evaluator boundary, not an OS sandbox: same-user agents must not
  have unrestricted access to the harness process, interpreter, or output dir.
- **Media control is unsupported with the current producer.** The media checker
  always fails closed with an explicit unsupported-evidence diagnostic.
  `recordDispatch` stores handler `conversationID` as `session_id`; executor
  events use the loop accounting conversation, possibly a thread/step identity.
  Require producer-issued turn/task/step/thread association plus fetched URL
  and content evidence before implementing ingestion acceptance. A matching
  conversation and successful `transcript_fetch` alone are insufficient.
- **No network is used silently.** `media-transcript-control` requires real
  `transcript_fetch` tool evidence (success, uncached, same conversation) and
  is expected BLOCKED until transcript tooling/network access is explicitly
  approved for the scratch rig AND producer evidence is repaired; it is tagged `requires-media-network` +
  `blocked-until-media-preflight`. There is no canned transcript fixture.
  Note `cached_fetch` does not wrap `transcript_fetch`; do not treat a
  cached-fetch gate as media coverage.
- **Reminder control requires a dedicated scratch daemon.** The checker is
  read-only over that daemon's `jobs.json` (point `MEEPT_ROUTING_JOBS_FILE`
  at the scratch daemon's data dir; no production default). Reminder jobs
  must be type=reminder, enabled, correct schedule/message, created during
  this turn's window, and unique.
- **Run it** (all nine tasks in one run, one --out dir, once):

  ```sh
  MEEPT_ROUTING_JOBS_FILE=~/.meept-scratch/jobs.json \
    /tmp/meept-bench-bin run --suite suites/routing-repair.json \
    --out results/routing-repair-run1 --auto-approved
  ```

  `MEEPT_ROUTING_RUN_DIR` (absolute, defaults to the run's `--out`) names the
  directory whose `transcripts/` the outcome probes read; always use a fresh
  output dir per acceptance run. Offline verifier for the deterministic
  judge + outcome probes (no daemon, no model):

  ```sh
  go test -p 2 ./internal/suite -run TestRoutingRepair -count=1
  python3 -B suites/routing-repair-data/checks_test.py
  ```
- **Answer checks are deterministic** (`suites/routing-repair-data/judge.py`
  via the existing `llm_judge`/`--judge-cmd` contract, e.g. `--judge-cmd
  'python3 -B suites/routing-repair-data/judge.py'`), not an LLM judge; they
  grade the arithmetic and path-defect replies without burning model calls.
- Case classes (tags): `arithmetic-path`, `media-data`, `locative-time`,
  `polite-prefix` — each with a `negative` case and a `control` (positive)
  case, so an overbroad guard repair is caught. Route assertions
  (`expect_agent` / `expect_intent` / `forbidden_classification_methods`)
  are separate from artifact/outcome checks; a completed misrouted turn
  still records outcome results and cannot pass.
- Frozen case list (do not reword prompts; the wording IS the test):

  | id | class | role | route assertion |
  |---|---|---|---|
  | path-question | arithmetic-path | negative | forbids short_message_guard |
  | arithmetic-control | arithmetic-path | control | intent chat |
  | media-url-as-data | media-data | negative | agent coder, intent code, forbids media_url_guard |
  | media-transcript-control | media-data | control | agent analyst, intent analyze (unsupported producer evidence) |
  | locative-file | locative-time | negative | agent coder, intent code |
  | timed-reminder-control | locative-time | control | agent scheduler, intent schedule |
  | polite-file-punctuated | polite-prefix | negative | agent coder, intent code |
  | polite-file-plain | polite-prefix | negative | agent coder, intent code |
  | git-status-control | polite-prefix | control | agent committer, intent git |

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

The baseline **is committed**: `results/baseline/regression.jsonl` pins the
gate. `.gitignore` excludes `/results/*` but negates `results/baseline/`, so
only baseline JSONLs are tracked. Diff against it before merging changes that
touch dispatch, memory, or the bus:

```sh
/tmp/meept-bench-bin diff --baseline results/baseline/regression.jsonl \
                          --current  results/<new-run>/results.jsonl
```

Never edit the baseline to make a red gate green — see *Flake policy* below.

## Flake policy

- A task failing <1 in 5 local runs = flaky: tag "flaky", keep in suite.
- A task failing deterministically = real regression: diff gate fires; fix
  meept or fix the task, never the baseline.
- **Classifier capacity gate:** the daemon's `classifier` alias needs at
  least one healthy candidate (local llama.cpp on 127.0.0.1:8080, agnes,
  zai, or ollama). When ALL are dead/rate-limited, intent analysis fails,
  dispatch falls to keyword heuristics, and any `expect_agent` assertion can
  flip (writer/committer/image-gen misroutes). Symptom in results:
  `routing mismatch: expect_agent=coder routed=<other>` across unrelated
  tasks in one run. Remedy: start the local runtime or free up a provider,
  then re-run — the suite itself is correct.

A gate nobody has tested failing is decoration — the injected-fault drill
(clearing the committed baseline's `file-write-routes-coder` checker pattern,
full suite run, `diff` exit 1, restore, clean re-verify) proves the gate has
teeth. When the gate goes red on a real change:

- **Failing < 1 in 5 local runs = flaky.** Tag it `flaky` in
  `suites/regression.json` and keep it in the suite. The runner has no tag
  filter yet (`Manifest.Select` matches ID substrings only), so until
  `--ignore-tags flaky` lands, exclude a flaky task from a gate run
  manually with `--task` — running the suite in two passes and diffing the
  concatenated results — or let it fail: a tag alone does not turn the gate
  red, the diff does.
- **Failing deterministically = real regression.** The diff gate fires:
  fix meept or fix the task. **Never fix the baseline.**

## Known-failures

| Task | Tag | Reason |
|------|-----|--------|
| `memory-recall-marker` | `known-failure` + `xfail: ...` encoded in tags | meept cross-conversation recall gap: daemon memory is global, but the dispatcher only auto-injects memories scoring > 0.3 relevance and the FTS scores this marker fact at ~0.2, so fresh-conversation recall silently drops it. Checker accepts either the codeword or an explicit `MEMORY-UNAVAILABLE` admission so the suite completes while the gap is open. Keep LAST in the task array. |

## Cost note

Rows show `$0.00` on free-tier providers; the cost delta columns in `diff`
still work — they compare the recorded values, so any nonzero-cost provider
change shows up as a delta regardless of absolute magnitudes.
