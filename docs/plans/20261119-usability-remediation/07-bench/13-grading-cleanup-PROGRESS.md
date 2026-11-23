# 13 - grading cleanup: PROGRESS

Run: 2026-11-23. Bench tree: /Users/caimlas/git/meept-bench/.hermes/worktrees/usability-acceptance (a85b074). Meept main: /Users/caimlas/git/meept.

## A. u-fail-nonexistent-read verdict variance run5/6 ('fail') vs run7 ('error')

Evidence gathered:
- provenance-run5.json: meept=4f934df, bench=b92ee71, same daemon binary sha as run6.
- provenance-run6.json: meept=4f934df, bench=d334cc7 (only change: runner subscribes turn.terminal + routing test pins).
- provenance-run7.json: meept=77fa218 (rebuilt), bench=a85b074.
- run5 transcript: model ran real file tools (file_read evidence + shell ls/find); evidence gate silent; task COMPLETED with a clean "file does not exist" reply -> harness graded checks: exit_zero PASS, terminal_delivery FAIL (turn.terminal not yet captured pre-fab82fb), llm_judge FAIL (reasoning-prefix parse bug, pre-21dd263) -> verdict 'fail'.
- run6 transcript: same daemon binary; model hit the file-evidence gate revision loop ("Rejected: no file operation ... after a retry") -> terminal status failed -> 'error'.
- run7 transcript: model TYPO-CREATED "definely_missing_12345.txt" (echo+file_write+file_read), claimed "file_read confirmed the file exists" in its evidence JSON -> meept 77fa218 includes 57be948 "completion honesty" (errored/unbacked steps rejected) + the existing fileEvidenceGate -> step failed, terminal status failed -> 'error'.
- Harness-side verdict logic (runner.go lines 436-441: resp.Error != "" -> 'error') is IDENTICAL across b92ee71..a85b074 (diff shows only turn.terminal subscription + auto-approver additions). checkers/ untouched between runs.

Disposition: MODEL-SIDE variance, not harness grading drift. The bench graded each run correctly under its then-current harness; the daemon behavior changed (run5: honest tools; run6: gate revision loop; run7: honesty gate + evidence-JSON failure relay). run7's 'error' is the CORRECT honest grade (the task instructed "tell me plainly it does not exist"; the model fabricated a file instead). No harness fix; no retries to be added. To be documented in REPORT.

## B. judge-local.py empty-reply robustness + unit test

- "cannot find score in judge output: '<think></think> 0.9'" errors (run1/2/4/5, 3+ tasks) were ALL the reasoning-prefix shape -> already fixed by 21dd263 (re.sub strips empty think block).
- Empty AGENT replies take NO different parse path in judge-local.py: the answer is forwarded verbatim; only the judge MODEL's completion is parsed. Verified judge-local.py handles empty model completion via `if not line: return fail("empty judge completion")` (clean exit 1, no traceback).
- Remaining gap found and fixed: no unit test pinned these behaviors. ACTION DONE: extended scripts/test_judge_local.py with 5 new cases:
  (1) empty judge completion -> clean exit 1 "empty judge completion", no traceback;
  (2) whitespace-only completion -> same;
  (3) EMPTY agent reply -> graded 0.00 (never an error);
  (4) reasoning-prefixed score with and without trailing space -> parses;
  (5) non-empty <think> body + score -> fails cleanly (documented limitation: the 21dd263 strip only handles EMPTY think blocks).
- Selftest run: python3 scripts/test_judge_local.py -> PASS (all cases green; no judge-local.py change needed -- its parse path was already robust).
- Note: no Makefile selftest target exists for judge-local; the script itself is the selftest.

## C. u-context-two-step 8B plan-routing (run7) -- DISPOSITION: model-bound, documented

Facts:
- run7 primary ack: intent_type=code, classified_by=heuristic_fallback, status=parked, "mode: planned". run5/6 same prompt classified quickplan @0.3 via "fallback" (ALL classifiers failed) -> "mode: quick plan".
- Run7's LLM classifier was unavailable for this turn (heuristic_fallback = degraded path); run7-final's u-direct-explicit DID get llm classification, so the classifier ran sometimes -- per-turn availability variance (rig-router sidecar + classifier_boot_fail_fast=true).
- Routing chain (dispatcher.go): "Create a file..." matches simpleCodeKeywords -> IntentCode@0.55 heuristic_fallback -> RequiresPlanning()=true -> suggestMode: analysis nil -> IntentCode.SuggestedMode()="plan"; prompt is 137 chars so the short-input downgrade (<50 chars -> "direct") CANNOT fire. Same for the 148-char steer turn.
- The trivial-input guard the parent asked about is trivial_lane.go isTrivialQuestion + suggestMode's len(input)<50 downgrade. Neither fires: the guard vetoes leading execution imperatives BY DESIGN ("create a file..." is artifact work, not a trivial question), and the prompt exceeds 50 chars. So the guard "missing" this input is correct behavior, not a defect.
- The plan machinery then degraded honestly: 0 subtasks, "[planning degraded: single direct step]" (run7-final ack), and the file write DID execute (run7 file_contains ^7$ PASSED).
- The run7-final verdict 'fail' came from the steer turn: the 8B model claimed the counter.txt edit in an evidence JSON envelope without a verified successful tool op; the review evidence gate rejected the revision; terminal_delivery missed the steer terminal.
- Low-risk fix assessment: no prompt/threshold one-liner exists that helps. SuggestMode short-input downgrade at a higher threshold (e.g. <150 -> direct for plain single-file requests) would misroute genuinely multi-step requests and contradicts the guard's own documented veto ("artifact verbs always keep task routing"). The plan-route itself is not the failure: planning degraded to a single direct step and executed. The failure is the 8B model narrating completion without verifiable tool evidence -- model quality bound.

Disposition: MODEL-BOUND. Documented; no harness/daemon change made.

## Checkpoints
- [x] A root-cause complete (model/daemon-side; harness grading logic identical across b92ee71..a85b074)
- [x] B unit test extended + selftest PASS (judge-local.py unchanged -- already robust)
- [x] C disposition: model-bound, documented
- [x] go test ./internal/suite/ ./internal/checkers/ -count=1 (bench): PASS
- [x] go build ./... (meept): PASS (no meept files changed, build clean anyway)
- [ ] REPORT.md appended
