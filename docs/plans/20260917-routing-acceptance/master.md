# Routing acceptance companion - plan orchestrator

Prepare only. Do not implement, stage, or commit without execution approval.

## Meta

Root; parent none; three leaves. Baseline: meept-bench 67d3093.
Existing working change: meept-bench-bin. Do not modify this binary.
Companion producer plan: /Users/caimlas/git/meept/docs/plans/20260917-routing-repair/master.md.

## Goal

Move live regression acceptance into meept-bench. Keep dispatcher fixes, offline classifier evaluation, corpus builders, and embedding-cache repairs in meept.

This tree first makes routing evidence trustworthy, then adds synthetic regression cases, then verifies repaired meept on an isolated local rig. No private replay corpus moves between repositories.

## Architecture

Use the existing suite loader, asynchronous chat client, runner, and artifact checkers. A submission acknowledgement is not completion. ChatAsync already awaits turn.terminal; retain this behavior.

Current suites support expect_agent. Transcripts record agent and classification method. The runner queries each separately from the latest dispatch and skips assertions when the agent is empty. These are source-confirmed risks, not yet executed reproductions.

## Interface Contracts

### C1: repository ownership

Meept owns producing route decisions with stable identifiers and actual serving-model provenance. Bench owns consuming evidence and reporting acceptance. Bench must not import meept internal packages or copy private metrics databases.

Leaf 01 must inspect the current session.dispatch_trace response before freezing its decoder. Current bench clients only decode agent_id or classifier_method from limit=1 responses. Intent and turn identity availability are NOT established. Missing required fields block the affected assertions; never invent a daemon API.

### C2: proposed optional expectations

Keep expect_agent. Add optional expect_intent and forbidden_classification_methods to suite Task. The latter is an array of method names, for example ["short_message_guard"]. No expectation means legacy outcome-only behavior.

Record one routing observation per evaluated turn with explicit availability and identity. Proposed internal observation fields: agent_id, intent, classification_method, turn_id, dispatch_id, and evidence_status. These are a bench design, not a claim about the current wire response.

When an expectation exists, missing required evidence produces an error, not a pass. A known mismatch fails routing. Run outcome checkers after a completed turn even when routing fails; retain both results. Overall pass requires every requested routing assertion and outcome check to pass. No failed or unavailable route can become success through an artifact checker.

### C3: evidence association

Read agent, intent, and method from one dispatch record, not separate latest-record calls. Prefer turn identity. Until the daemon exposes a usable stable association, permit only a fresh session with one submitted turn and a verified unique dispatch. Multiple or ambiguous entries mean unavailable evidence. Never use timestamps alone to claim exact identity.

### C4: synthetic fixtures and frozen cases

Use new synthetic inputs and small deterministic repository fixtures. Do not copy transcript phrases from private replay data. Keep the diagnostic trigger intact; adding helper instructions can change routing and invalidate the test.

The regression suite is a repair test, not unseen model-quality evidence. Select no policy on this suite and then claim independent acceptance accuracy. No modeled 0.868 chain credit belongs in bench outcome scores.

### C5: runs and publication

Use a fresh output directory per acceptance run. Every summary states expected, attempted, completed, passed, failed, error, and timeout counts. Missing tasks invalidate full-suite acceptance even when the existing diff command permits partial reruns.

Raw transcripts remain private. Public evidence contains case IDs, sanitized checks, source revisions, suite hash, run identity, and model provenance. Model override strings do not prove which model served a request. Unknown provenance is reported as unknown.

## Child Index

| Leaf | Document | Dependencies | Context | Effort |
|---|---|---|---|---|
| 01 | [Routing evidence](01-routing-evidence.md) | execution approval; daemon contract inspection | 60K | 3-6 hours |
| 02 | [Synthetic regression suite](02-regression-suite.md) | 01 reviewed; meept guard repairs for green acceptance | 45K | 2-4 hours |
| 03 | [Isolated acceptance](03-live-acceptance.md) | 01-02 reviewed; meept repair offline gates | 50K | 2-4 hours plus model time |

Estimated core effort: 7-14 engineering hours plus live execution. Dispatch serially because the runner contract precedes cases and acceptance. Do not add workers merely for parallelism.

## Dispatch Protocol

1. Obtain execution approval and recheck both repositories. Reconcile overlapping commits and working changes before dispatch.
2. Dispatch one checkpoint at a time with its leaf text and contracts. Workers must not stage, commit, install, push, or operate production daemons.
3. Require failing production-path tests before repairs. Parent independently reviews output and runs scoped checks.
4. Stop when a required daemon evidence field is missing. Return an exact producer requirement to the meept plan; do not fabricate bench-side evidence.
5. Run live acceptance only after offline gates and producer repairs. Scratch local-model operations are approved; production restarts and cloud use are not.

## Review Checklist

- [ ] Unknown routing evidence cannot pass a requested routing assertion.
- [ ] Route fields belong to one dispatch and one evaluated turn.
- [ ] Outcome checks still run for completed misrouted turns.
- [ ] Synthetic case wording preserves each failure trigger and includes controls.
- [ ] Final report distinguishes code repair, live outcome, and model-quality claims.

## Coding Conventions

- Use the existing Go 1.24 module and standard library patterns. No new dependency without approval.
- Locate symbols before reading source. Apply edits with patch/write_file. Preserve unrelated changes.
- Bound Go package concurrency with -p 2. Only the parent runs broad suites.
- Extend existing JSON records compatibly; test old manifests, rows, and transcripts.
- Use scoped file ownership. Split a checkpoint before more than three change files or an unverified interface expansion.

## Completion Tracking Table

| Leaf | Status | Evidence |
|---|---|---|
| 01 | REVIEWED | Parent source review and go test -p 2 ./... pass; no commit; multi-turn identity explicitly unavailable |
| 02 | VERIFIED_OFFLINE | Parent full tests, five-package race tests, vet, build and focused replacement/legitimate-work tests pass. Nine unique cases. Trusted checker bytes stay outside the worktree. Media positive control remains unsupported, not accepted. |
| 03 | IN_PROGRESS | Pre-fix live reproduction complete on isolated scratch rig (2026-09-17): path-question misrouted live — dispatch record shows agent=chat intent=chat method=short_message_guard conf=0.9; bench row verdict=fail error_kind=routing_mismatch via forbidden_classification_methods; evidence_status=available association=fresh_session_unique_dispatch; chat agent deflected to user. Rig: single local 8B (port 63108), provenance in rig provenance.json. Remaining: post-repair two full acceptance passes; media control stays BLOCKED (producer evidence). |

## Integration Test Plan

During implementation run `go test -p 2 ./internal/daemonclient ./internal/suite ./internal/runner ./internal/results ./internal/checkers ./internal/scorecard ./internal/diff`, followed by `go test -p 2 ./...` and `go vet ./...`.

Build with `go build -o /tmp/meept-bench-routing ./cmd/meept-bench`. Check run/diff help before freezing live commands. Execute two fresh complete runs of the new routing suite on the repaired scratch daemon. Verify exact task coverage and all attempts; best-attempt collapse must not conceal failures.

Keep the existing regression suite and baseline unchanged until new cases pass independently. Promotion into the existing gate is a separate reviewed change, not automatic baseline replacement.

## Follow-up scope, not silently omitted

Completion-gated session-state scenarios require a second tree. Current Turns uses delay_s, so timing alone cannot establish an approved plan or completed prior action. Do not claim quickplan-versus-code state coverage from this single-turn suite.

A general run-manifest/export facility also belongs in bench, but exceeds the core transfer. This tree records sanitized provenance for its acceptance runs. A later tree can generalize publication safety, actual-model evidence, and run-scoped aggregation after the daemon contract is known.

Undefined metric null/n/a semantics should be audited in bench before transfer; no bench scoring defect is established by the meept Python defect.

## Open Questions

- Does the current daemon expose intent and stable dispatch-to-turn identity? Leaf 01 answers from source and a local protocol fixture.
- Can supported fixture setup seed exact files before the primary prompt? Leaf 02 inspects isolation; add a minimal setup capability only if necessary.
- Is a local-only media-summary positive control possible with existing cached tools? Otherwise report that control blocked; do not enable external network access silently.

## Preparation record

Only these plan documents and the root handoff note are created during preparation. No source, suite, baseline, daemon, or skill change is required.
