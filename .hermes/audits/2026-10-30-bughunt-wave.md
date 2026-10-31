# meept-bench bughunt wave 2026-10-30 — full-repo audit (no prior wave)

Method: 2 read-only auditors (core harness isolate/runner/daemonclient/main;
scoring layer checkers/diff/fixtures/scorecard) + parent verification.
Baseline gate: go build ./... OK; go test -p 2 -count=1 ./... ALL GREEN.

## Parent-verified findings

1. [HIGH] cmd/meept-bench/main.go:151-153 — `run` exits 0 when tasks fail.
   `RunSuite` error + partial rows falls through to scorecard and exit 0.
   No per-row pass check at all. CI gate (docs/plans/phase-2/master.md:68)
   wires the `diff` exit code only; a suite run with failures reports
   success. Fix: exit 1 when any row fails (opt-in `--strict` if the
   "completed != passed" contract is intentional).
2. [MEDIUM] internal/isolate/isolate.go:24-30 — `NewManager` accepts a
   RELATIVE scratch root; worktree dirs then land under CWD. A bench run
   from inside the repo puts worktrees inside the repo tree. Fix:
   filepath.Abs + reject symlinked root escaping the declared prefix.
3. [MEDIUM] internal/isolate/isolate.go:58 — deterministic worktree name
   (`sanitize(name)`): two concurrent `run` invocations on the same scratch
   root collide (RemoveAll + branch -D race). Fix: per-PID suffix or flock.
4. [MEDIUM] cmd/meept-bench/main.go:112 — `--attempts` unvalidated (0 or
   negative accepted; zero-check run exits 0 — vacuous green).
5. [LOW] daemonclient single retry; capture.go exits 1 on fetch failure
   (fine); isolation guard verified solid (os.Exit(2) + subprocess test).
6. [PASS] checkers transcript scoping (d77256d): checkers cannot read
   previous run dirs — auditor verified path pinning.
7. [PASS] diff median-of-N baseline + regression exit 1 — correct per
   04-diff-command.md contract.

## Auditor claims marked [UNVERIFIED] by parent
- isolate.go symlinked m.Root bypass (1.1): plausible but no PoC shown;
  fold into fix #2 validation regardless.
- runner context-cancel orphan worktrees (2.1): not reproduced; teardown
  exists on ApplySetup failure path.

## DISCLOSURE LEDGER (auditor noise)
- bench-a1 finding 1.3 (env-var override): speculative, no such env var
  exists in the code — rejected.
- bench-a1 finding 2.3 (scratch under $HOME): by design (flag help text
  says keep under ~ for daemon allowlists) — rejected.

## Fix disposition (fix-all authorized)
- FIXING: #1 exit code, #2 abs+symlink validation, #3 collision guard,
  #4 attempts validation — one fixer, scoped to cmd/meept-bench +
  internal/isolate.
- DEFER (logged): daemonclient retry breadth, orphan-worktree sweep.
