# CI Workflow - Implementation Leaf

> **For the implementing agent:** You are the implementer for this leaf.
> Implement ALL tasks below. Do NOT commit — the orchestrator handles all
> git operations after review. Do NOT push or trigger workflows.

## Meta

- **Parent:** ../master.md
- **Scope:** GitHub Actions workflow building meept-daemon + meept-bench and
  running the suites on a schedule; contract 4 in ../master.md.
- **Dependencies:** 01, 02, 03 (regression.json must exist), 04 (diff gate).
  May be AUTHORED in parallel but only marked done after those land.
- **Estimated Context:** 50K
- **Concurrency Group:** D

## Goal

Bench runs happen without a human: per-push smoke, nightly full, diff-gated
against the last green baseline. Results are retained as artifacts so
baselines are recoverable.

## Context

- Runner needs a live meept-daemon. In CI: build meept from source at a
  pinned ref (env `MEEPT_DAEMON_REF`, default `main` of
  /Users/caimlas/git/meept — the LOCAL path; in CI use the git URL
  `https://github.com/bhodgens/meept` — verify the actual remote with
  `git -C /Users/caimlas/git/meept remote -v` and use that), launch
  `meept-daemon -s <sock> -d <datadir> -f` in the background, wait for the
  socket, then run the bench.
- Daemon config: CI needs a minimal `~/.meept/models.json5` with a REAL
  provider key. GitHub secrets: `MEEPT_CI_PROVIDER_KEY` (user must add it).
  The workflow writes a minimal models.json5 from the secret at run time.
  NEVER echo the secret. If the secret is absent, jobs fail fast with a
  clear message.
- Auto-approval: the runner flags `--auto-approved` exists; GAPS.md gap 6
  requires disclosure — rows already record `auto_approved`; the workflow
  also sets env AUTO_APPROVED=1 for visibility in logs.
- Scratch root: `--scratch "$HOME/.meept-bench-ci"` (workspace-local).
- HF token: not needed this phase (LongMemEval template tasks don't fetch).

## Tasks

### Task 1: workflow file

Create `.github/workflows/bench.yml`:

- **push job (smoke):** on push to main. Steps: checkout this repo;
  setup-go (version from go.mod); clone meept at `$MEEPT_DAEMON_REF`;
  `go build -o ./bin/meept-daemon ./cmd/meept-daemon` in the meept clone;
  build bench; start daemon (`nohup ./bin/meept-daemon -s /tmp/meept.sock
  -d /tmp/meept-data -f &` with a minimal models.json5 from secret); wait
  for socket (retry loop, 30s max); run `./meept-bench doctor`; run
  `./meept-bench run --suite suites/smoke.json --attempts 1 --scratch
  "$HOME/.meept-bench-ci"`; upload `results/` as artifact
  `bench-results-${{ github.run_id }}` (retention 90 days).
- **nightly job (full):** cron `0 7 * * *` (07:00 UTC). Same setup, then
  smoke + `suites/regression.json` (when it exists; guard with an
  `if [ -f suites/regression.json ]`).
- **diff gate step (nightly only):** restore the last green baseline JSONL
  from artifacts of the previous successful nightly run
  (`actions/download-artifact@v4` with `run-id` + `github.token` — pattern
  from the actions docs for cross-run download; if flaky, fall back to
  committing `results/baseline/smoke.jsonl` to a `bench-baselines` branch
  and checking it out — prefer the branch approach for reliability and
  document it in the workflow comments); run
  `./meept-bench diff --baseline <baseline> --current
  results/smoke/results.jsonl`; on exit 1, fail the job with a summary
  printed from the diff output.
- After a GREEN nightly: upload the new results.jsonl as the baseline
  (branch approach: a small script `scripts/update-baseline.sh` that
  commits results to the `bench-baselines` branch using the
  `github-actions[bot]` identity). NEVER let a red run update the baseline.

### Task 2: baseline script

`scripts/update-baseline.sh <results.jsonl>`: verifies the source run was
green (every row verdict==pass or explicitly tolerated), then force-pushes
the file to the `bench-baselines` branch under `baselines/<suite>.jsonl`.
Uses `git worktree` on a temp dir to avoid disturbing the checkout. Bash
strict mode. Test locally with a fixture file and a scratch clone of this
repo (init a bare repo in /tmp as origin stand-in).

### Task 3: daemon readiness probe

Inline step or `scripts/wait-for-daemon.sh <socket> <timeout-seconds>`:
loops until `meept-bench doctor` succeeds (doctor already pings the socket
via MEEPT_BENCH_SOCKET env). Use it in both jobs.

## Verification

1. `actionlint` if available; else `python3 -c "import yaml,sys;
   yaml.safe_load(open('.github/workflows/bench.yml'))"` for syntax.
2. Local dry-run of the daemon-start steps: build meept daemon, run the
   wait script against a manually started daemon, confirm doctor passes.
3. Local dry-run of update-baseline.sh against a /tmp bare repo.
4. Do NOT push; workflow correctness against real CI is verified by the
   orchestrator after merge.

## Report

Report: files created, syntax-check output, local dry-run outputs,
secrets the user must configure (exact names), deviations.
