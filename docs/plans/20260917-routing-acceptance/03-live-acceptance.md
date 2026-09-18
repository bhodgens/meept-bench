# Isolated live acceptance - verification leaf

DISPATCH INSTRUCTION: Run only after leaves 01-02 and meept repair offline gates pass. Do NOT commit. Scratch local-model verification is approved; implementation dispatch remains pending.

## Meta

Parent: [master.md](master.md). Scope: complete live evidence through meept-bench.
Dependencies: 01-routing-evidence.md, 02-regression-suite.md, and reviewed meept fixes.
Estimated Context: 50K. Effort: 2-4 hours plus measured model execution time.

## Context

Bench already submits chat asynchronously and awaits terminal events. Keep this path. Current Makefile supports REPO, SCRATCH, MODEL, and ATTEMPTS. Build to /tmp, not the modified tracked meept-bench-bin.

The original meept repair plan remains responsible for building and configuring the scratch daemon. This leaf owns bench execution and acceptance reporting. Do not create a second daemon harness or modify installed runtime models.

## Interface Contracts (From Parent)

Use a fresh output directory per run. Count every expected task and attempt. Partial output is incomplete even if diff reports no regression. A best-attempt score must not conceal failed attempts.

A model name passed on a request does not prove serving-model identity. Obtain producer evidence or mark the field unknown. Record source revisions, suite content hash, configuration fingerprint without secrets, and local model fingerprints where available.

Publish no raw prompts, replies, tool payloads, credential values, or sensitive paths from real traffic. Synthetic fixture transcripts remain local unless separately reviewed for publication. Commit no generated evidence without approval.

## Tasks

### Task 1: prepare the reviewed runner

1. Run `go test -p 2 ./...` and `go vet ./...` in meept-bench. Record failures before live execution.
2. Build with `go build -o /tmp/meept-bench-routing ./cmd/meept-bench`.
3. Run `/tmp/meept-bench-routing run --help` and `/tmp/meept-bench-routing diff --help` to verify current flags.
4. Verify the scratch socket, repo-under-test, fixture availability, and local providers from the meept owner handoff.
5. Record the baseline identities. Do not start if the daemon binary or serving model differs from the reviewed configuration.

### Task 2: run two complete acceptance passes

Command template, after help validation:

`MEEPT_BENCH_SOCKET=<scratch-home>/meept.sock /tmp/meept-bench-routing run --suite suites/routing-repair.json --repo <reviewed-fixture-repo> --scratch <fresh-worktree-root> --out <fresh-output-dir> --attempts 1 --keep-failed`

Angle-bracket values are operator paths, not literal commands. Resolve and record them before execution. Confirm availability of --keep-failed from help. Do not use auto-approval or a judge command unless the approved suite needs those mechanisms.

1. Run pass one through real local models, awaiting terminal completion.
2. Verify output contains exactly the frozen task IDs and one attempt each. Diagnose missing or duplicate rows before scoring.
3. Run pass two into a different fresh output directory with the same frozen inputs and model identities.
4. Run diff on the two JSONL files, then independently require complete task coverage and no failures, errors, or timeouts in either pass.
5. Report routing checks, task outcomes, and unknown evidence separately. Do not infer model quality from two small regression runs.

If a control requires unapproved external access, record BLOCKED. Do not silently omit the task and call the full suite green.

### Task 3: publish a sanitized acceptance record

Proposed documentation: docs/RUNBOOK.md additions and docs/plans/20260917-routing-acceptance/verification.md.

1. Record source revisions, suite hash, attempt counts, elapsed time, outcome counts, and evidence availability.
2. Link each case to its meept fix and offline reproduction test, using case IDs rather than private input excerpts.
3. Document the difference between route-correct/task-failed and route-wrong/task-completed outcomes.
4. List all blocked controls, missing model provenance, and incomplete coverage explicitly.
5. Recommend promotion into the existing regression gate only after review of both full runs. Do not replace the existing baseline automatically.

### Task 4: reconcile ownership with the meept plan

Return a compact evidence handoff to /Users/caimlas/git/meept/docs/plans/20260917-routing-repair/05-acceptance.md through the parent. The bench worker does not edit the meept repository.

Include verified cases, exact runner commands, source/model identities, artifact locations, unavailable assertions, and outstanding producer requirements. Historical privacy containment and the model-quality campaign remain outside this acceptance leaf.

## Self-Verification Checklist

- [ ] Both runs use fresh directories and complete frozen case coverage.
- [ ] Terminal outcomes, routing evidence, and artifact checks are recorded separately.
- [ ] Model identities identify the server or remain explicitly unknown.
- [ ] No production restart, cloud request, install, or baseline overwrite occurs.
- [ ] Raw evidence remains local and the public report is sanitized. Do NOT commit.

## Review Checklist

- [ ] Parent verifies row counts and every error state, not just diff exit status.
- [ ] Missing evidence cannot become a pass.
- [ ] Both full runs support any stability claim.
- [ ] The meept plan consumes the bench report rather than rerunning another live harness.
- [ ] Return VERIFIED_LIVE, VERIFIED_OFFLINE, or BLOCKED with exact evidence.
