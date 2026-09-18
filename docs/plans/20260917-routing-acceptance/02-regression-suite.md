# Synthetic routing regression suite - implementation leaf

DISPATCH INSTRUCTION: Execute only after leaf 01 review and execution approval. Do NOT commit or stage files.

## Meta

Parent: [master.md](master.md). Scope: synthetic live regression cases and controls.
Dependencies: 01-routing-evidence.md; meept guard repairs before green acceptance.
Estimated Context: 45K. Effort: 2-4 hours.

## Context

The existing regression suite combines expect_agent with file_contains and exit_zero checks. This new suite must preserve the diagnostic input shape. An extra instruction can accidentally bypass the guard being tested.

Proposed files: suites/routing-repair.json and internal/suite/routing_repair_manifest_test.go. Fixture files are new and must be listed explicitly after setup requirements are verified. Inspect internal/isolate before choosing fixture placement. Worktrees based on HEAD do not contain uncommitted fixtures.

## Interface Contracts (From Parent)

Use leaf 01 expectation fields. Check observable work separately from routing. Use synthetic text, never private replay excerpts. Freeze prompts before evaluating fixes. Report these as regression cases, not unseen model-quality evaluation.

The suite includes the following paired case classes. Final case IDs and manifest checks must be frozen in this leaf before live execution.

| Class | Negative case | Positive control | Required evidence |
|---|---|---|---|
| Arithmetic/path | Question containing 'what is' and a source path | Actual arithmetic question | Path case forbids short_message_guard; arithmetic control remains correct |
| Media/data | Write a unit test using a YouTube URL as data | Consume a video transcript | Coding case forbids media_url_guard and checks produced test; media control checks ingestion |
| Locative/time | Create a file at a named path | Real timed reminder | File case is not schedule and produces content; reminder remains scheduling |
| Polite prefix | Punctuated versus unpunctuated polite file request | Genuine git action | Both file cases produce the artifact and avoid unsupported git routing |

For file requests, expect_agent=coder only where the current canonical lane mapping establishes coder as the required destination. For question-only cases, do not force an agent merely because the guard must fall through. Forbid the wrong method and check the answer independently.

## Tasks

### Task 1: freeze fixtures and observable checks

1. Inspect isolate setup and existing synthetic fixture conventions. Do not assume a new setup field exists.
2. Select a deterministic source defect for the path question and a harmless URL-processing task for the media case.
3. Define exact artifact or answer checks. Avoid asking the model to run a checker whose expected output is disclosed in the prompt.
4. Determine whether transcript consumption can use existing local cached tooling. If unavailable, mark the media positive control blocked rather than allowing network silently.
5. Publish the fixed case list and setup requirements to the parent before changing runner setup.

Any new generic setup capability requires a separate checkpoint with production-path tests and explicit file ownership. Do not hide setup work inside prompt prefixes, because prefixes change classification.

### Task 2: add the manifest and loader tests

Files: suites/routing-repair.json and internal/suite/routing_repair_manifest_test.go.

1. Write a test loading the proposed suite with the production loader. Assert unique IDs, nonempty checks, explicit timeouts, and expectation coverage.
2. Run `go test -p 2 ./internal/suite -run TestRoutingRepairManifest -count=1`; confirm failure before adding the manifest.
3. Add all frozen cases with synthetic inputs and explicit tags identifying the failure class.
4. Assert both controls and negative cases exist. Ensure no mandatory expectation can silently disappear.
5. Rerun loader tests and all suite tests. Do not alter suites/regression.json or its baseline.

### Task 3: establish failure and repair evidence

1. Run available cases on an isolated pre-fix daemon only when the old binary and configuration are safely available. Never revert shared source.
2. Preserve pre-fix traces proving the intended failure; unavailable old binaries mean no live before/after claim.
3. After meept fixes, run the same frozen suite through leaf 03.
4. Record conditional failures separately: a live classifier may not emit the injected verdict needed to trigger a helper defect.
5. Retain meept's injected-verdict tests as proof of deterministic arbitration repairs, not as bench model predictions.

## Self-Verification Checklist

- [ ] Every case has a stable ID, explicit timeout, and an observable checker.
- [ ] Positive controls prevent an overbroad guard repair.
- [ ] Fixtures reach the isolated worktree without changing diagnostic prompts.
- [ ] No private text, production config change, or existing baseline edit occurs.
- [ ] Report blocked controls and exact loader test output. Do NOT commit.

## Review Checklist

- [ ] Parent confirms each prompt still contains the intended trigger.
- [ ] Route assertions do not confuse intent with agent identity.
- [ ] Answer/artifact checks discriminate success from a fluent non-answer.
- [ ] New setup capability, if needed, receives its own test and review checkpoint.
- [ ] Return APPROVED or concrete gaps before live acceptance.
