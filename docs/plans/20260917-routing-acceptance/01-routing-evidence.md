# Routing evidence - implementation leaf

DISPATCH INSTRUCTION: Execute only after companion-plan execution approval. Use tests before code. Do NOT commit or stage files.

## Meta

Parent: [master.md](master.md). Scope: reliable per-turn route assertions.
Dependencies: execution approval and daemon response inspection.
Estimated Context: 60K. Effort: 3-6 hours in serial checkpoints.

## Context

`internal/runner/runner.go:371` skips expect_agent when routed is empty. `internal/daemonclient/daemonclient.go:534-605` retrieves agent and method through separate latest-entry queries. These risks are source-verified, not runtime-reproduced.

`internal/results/results.go` already stores RoutedAgent and ClassificationMethod. `internal/suite/suite.go` already supports ExpectAgent. Extend these surfaces rather than adding another logger.

## Interface Contracts (From Parent)

Preserve expect_agent; add optional expect_intent and forbidden_classification_methods. Empty expectations preserve outcome-only behavior. Missing evidence for a requested assertion is an error. Known mismatch fails routing. Record routing and outcome checks separately; both must pass for overall success.

Read route fields from one dispatch record. Prefer stable turn identity. Fresh-session single-turn evidence is acceptable only when the dispatch is uniquely identified. Do not infer exact identity from timestamps or merge independent latest-record queries.

No assumed daemon API fields. Inspect the current producer before implementing the decoder. If identity or intent is absent, report the producer dependency and block affected assertions.

## Tasks

### Checkpoint 1: establish the evidence reader

Files: internal/daemonclient/daemonclient.go and internal/daemonclient/daemonclient_test.go.

1. Locate the current meept session.dispatch_trace producer and record its actual response shape with source references.
2. Add fake-daemon tests for one complete record, no records, method unavailable, multiple records, and mismatched turn identity.
3. Run `go test -p 2 ./internal/daemonclient -count=1`. Observe the failing identity/availability test before implementation.
4. Add one observation reader returning agent, intent, method, stable identifiers, and explicit availability. Keep legacy accessors compatible.
5. Rerun tests. Verify one response supplies all fields and unsupported identity cannot become success.

### Checkpoint 2: define optional manifest expectations

Files: internal/suite/suite.go, internal/suite/suite_test.go, internal/results/results.go.

1. Add manifest tests for optional intent and forbidden methods, empty entries, and old manifests.
2. Run `go test -p 2 ./internal/suite -count=1` and verify the new validation fails before repair.
3. Add the fields and validation without changing existing checker requirements.
4. Add backward-compatible routing observation/check fields to result records. Missing old fields remain unknown, not false evidence.
5. Run suite tests and compile dependent packages. Publish exact Go and JSON contracts before checkpoint 3.

### Checkpoint 3: enforce expectations and retain outcomes

Files: internal/runner/runner.go, internal/runner/runner_test.go, and results tests if needed.

1. Add a fake completed turn with unavailable routing and a declared expect_agent. Require an error rather than pass.
2. Add a known mismatch with a passing artifact checker. Require routing failure and retained passing outcome evidence.
3. Run `go test -p 2 ./internal/runner -count=1`; confirm failures before changing the runner.
4. Replace separate latest-record lookups with the observation reader. Run checkers after completed turns even when routing fails. Preserve the full transcript.
5. Test forbidden methods, intent mismatch, unknown intent, legacy no-expectation tasks, and transport failures. Rerun runner and daemonclient tests.

Do not apply artifact checkers to incomplete turns as if execution succeeded. Preserve failed terminal status independently from routing status.

## Self-Verification Checklist

- [ ] Missing route evidence fails a requested assertion.
- [ ] Agent, intent, and method refer to one dispatch.
- [ ] Known mismatch preserves outcome checks and transcript evidence.
- [ ] Old manifests and JSON rows remain readable.
- [ ] Report exact tests and unresolved daemon requirements. Do NOT commit.

## Review Checklist

- [ ] Parent reproduces the original missing-evidence behavior before accepting the fix.
- [ ] Fake-daemon responses match a source-verified wire contract.
- [ ] No stale latest-entry assumption survives for multi-turn sessions.
- [ ] No unrelated runner behavior or baseline changes occur.
- [ ] Return APPROVED or specific file-and-line gaps.
