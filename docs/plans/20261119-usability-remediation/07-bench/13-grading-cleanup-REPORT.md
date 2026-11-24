# 13 - grading cleanup: REPORT

Run: 2026-11-23. Bench: usability-acceptance worktree @ a85b074. Meept main @ main (unchanged). No commits made.

## A. u-fail-nonexistent-read verdict variance (run5/6 'fail'/'error' vs run7 'error')

Disposition: **DOCUMENTED -- model/daemon-side variance, harness grading correct in every run.**

Evidence:
- Provenance: run5 meept=4f934df/bench=b92ee71; run6 meept=4f934df/bench=d334cc7 (harness delta: turn.terminal subscription only); run7 meept=77fa218/bench=a85b074. The SAME daemon binary served runs 5 and 6.
- Harness-side verdict logic (runner.go: resp.Error != "" -> 'error', else checker-derived 'pass'/'fail') is byte-identical across b92ee71..a85b074; internal/checkers/ untouched between the runs. No grading-logic change can explain the variance.
- What actually differed (transcript diff):
  - run5: model honestly executed file tools (file_read + shell ls/find evidence), reported "The file does not exist", task COMPLETED. Graded 'fail' by the THEN-current harness only because terminal_delivery could not see terminals (pre-fab82fb) and the judge hit the reasoning-prefix parse bug (pre-21dd263). Under the CURRENT harness this behavior grades PASS (exit_zero + terminal_delivery + judge 0.9 all pass).
  - run6 (same daemon): model hit the file-evidence gate revision loop -> terminal failed -> 'error'.
  - run7 (meept 77fa218, includes 57be948 completion-honesty): model TYPO-CREATED "definely_missing_12345.txt", then narrated "file_read confirmed the file exists" in an evidence JSON envelope; the honesty/evidence gates failed the step -> terminal failed -> 'error'.
- run7's 'error' is the honest grade: the task instructed a plain "does not exist" answer; the model fabricated a file instead.

Conclusion: variance is seed/model-side behavior against an evolving-but-correct daemon gate set, not harness grading drift. Per instructions: no retries added, nothing to fix.

## B. judge-local.py empty-reply parse robustness + unit test

Disposition: **FIXED (test-side) -- judge-local.py itself was already robust; the missing piece was the pinning unit test.**

- All historical "cannot find score: '<think></think> 0.9'" judge errors (runs 1/2/4/5, 3+ tasks) were the reasoning-prefix shape; fixed by 21dd263.
- Empty AGENT replies take no special parse path: the answer is forwarded verbatim; only the judge model's completion is parsed. Empty/whitespace judge completions fail cleanly (`fail("empty judge completion")`, exit 1, one stderr line, no traceback).
- Extended scripts/test_judge_local.py with 5 cases: empty completion (clean exit 1), whitespace-only completion, EMPTY agent reply (graded 0.00, never an error), reasoning-prefix score with/without trailing space (parses), non-empty <think> body + score (fails cleanly; documented limitation -- the 21dd263 strip handles only EMPTY think blocks).
- Selftest: `python3 scripts/test_judge_local.py` -> PASS. No Makefile selftest target exists; the script is the selftest.

## C. u-context-two-step 8B plan-routing (run7)

Disposition: **MODEL-BOUND -- documented; no low-risk fix exists.**

- run7 primary ack: intent=code via heuristic_fallback (LLM classifier unavailable that turn; classifier_boot_fail_fast=true rig), "mode: planned". run5/6 got quickplan@0.3 via "fallback" (all classifiers failed). Per-turn classifier availability + verdict variance is the 8B/rig-router side.
- Trivial-input guard (trivial_lane.go isTrivialQuestion + suggestMode's <50-char direct downgrade) does not fire BY DESIGN: the guard vetoes leading execution imperatives ("create a file..."), and both prompts are 137/148 chars. Correct behavior, not a defect.
- The plan-route is harmless in practice: planning degraded to a single direct step ("[planning degraded: single direct step]", 0 subtasks) and the file write executed (run7 file_contains ^7$ PASSED). The run7-final 'fail' came from the steer turn: the 8B model claimed the counter.txt edit in an evidence JSON without a verified tool op; the review evidence gate rejected; terminal_delivery missed the steer terminal.
- Raising the short-input downgrade threshold (e.g. <150 chars -> direct) would misroute genuinely multi-step requests and contradicts the guard's documented veto; no safe prompt/threshold one-liner exists. The real residual is 8B-model narration-without-evidence, which the gates correctly police.

## Files modified
- scripts/test_judge_local.py (bench): +5 empty-reply/malformed-completion cases, doc header updated.
- docs/plans/20261119-usability-remediation/07-bench/13-grading-cleanup-PROGRESS.md (new).
- docs/plans/20261119-usability-remediation/07-bench/13-grading-cleanup-REPORT.md (this file).
- No meept files changed; nothing committed.

## Verification
- go test ./internal/suite/ ./internal/checkers/ -count=1 (bench): PASS.
- go build ./... (meept): PASS.
- python3 scripts/test_judge_local.py: PASS.
