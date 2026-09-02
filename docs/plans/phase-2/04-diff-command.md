# Scorecard Diff Command - Implementation Leaf

> **For the implementing agent:** You are the implementer for this leaf.
> Implement ALL tasks below using TDD. Do NOT commit — the orchestrator
> handles all git operations after review.

## Meta

- **Parent:** ../master.md
- **Scope:** New `meept-bench diff` subcommand comparing two results.jsonl
  files; contract 1 in ../master.md.
- **Dependencies:** none
- **Estimated Context:** 60K
- **Concurrency Group:** A

## Goal

Make regressions visible: compare a baseline results.jsonl against a current
one and exit 1 when any previously-passing task now fails. This exit code is
the CI gate (leaf 05 consumes it).

## Context

- Row shape: `internal/results/results.go` Row struct — fields TaskID,
  Attempt, Verdict ("pass"|"fail"|"error"|"timeout"), Passed, CostUSD,
  WallSeconds, TokensIn. Multiple rows per task when attempts > 1: diff on
  the BEST attempt per task (a task passes if any attempt passed).
- Existing subcommand wiring: `cmd/meept-bench/main.go` switch on os.Args[1]
  (version/doctor/run/scorecard). Add "diff" the same way.
- The scorecard command (`scorecardCmd`) is the closest sibling — mirror its
  flag style (`flag.NewFlagSet`, usage() text update).

## Tasks

### Task 1: diff core (internal/diff package, TDD)

Create `internal/diff/diff.go` + `diff_test.go`:

```go
package diff

// Result is one task-level comparison.
type TaskDiff struct {
    TaskID      string
    Baseline    string // best verdict in baseline ("pass"/"fail"/"absent")
    Current     string // best verdict in current
    Status      string // "unchanged-pass" | "unchanged-fail" | "regressed" | "fixed" | "new" | "removed"
    WallDelta   float64 // current wall - baseline wall (best attempt each)
    CostDelta   float64 // current cost - baseline cost
}

// Compare consumes raw JSONL row bytes (decoded by caller into
// []results.Row); best-attempt semantics: a task's verdict is "pass" if any
// attempt passed, else the last attempt's verdict.
func Compare(baseline, current []results.Row) []TaskDiff

// Summary aggregates: Regressed []string, Fixed []string, New []string,
// Removed []string, WallRegressed []string (tasks with WallDelta > +50% of
// baseline AND baseline wall > 5s — avoid noise on fast tasks).
type Summary struct { ... }
func Summarize(diffs []TaskDiff) Summary
```

Tests must cover: pass→pass, pass→fail (regressed), fail→pass (fixed),
new task, removed task, multi-attempt best-selection, wall-delta threshold
boundary (exactly +50% counts; +49% does not), empty baseline, empty
current, malformed JSONL → error.

### Task 2: JSONL loading

`internal/diff/load.go`: `LoadRows(path string) ([]results.Row, error)` —
line-by-line json.Decoder, skip blank lines, error with line number on bad
JSON. Tests: valid multi-line file, empty file, malformed line reports line
number.

### Task 3: CLI wiring

In `cmd/meept-bench/main.go`: `diffCmd(args []string)` with flags
`--baseline PATH` (required), `--current PATH` (required), `--json` (emit
Summary as JSON instead of text). Text output per contract 1:

```
REGRESSED (pass→fail): t3
FIXED (fail→pass):     t1 t7
NEW:                   t9
REMOVED:               t2
wall-time deltas > +50%: t4 (12.0s → 20.1s)
cost deltas:           t4 ($0.0100 → $0.0300)
```

Exit codes: 0 clean, 1 any regressed, 2 usage/IO error. Update usage()
text. Add cost deltas line only when non-zero deltas exist; use $%.4f.

### Task 4: end-to-end check

Build the binary; craft two small JSONL fixtures in /tmp; run:
- identical files → exit 0
- baseline pass / current fail on one task → exit 1, REGRESSED lists it
- `--json` emits parseable JSON (verify with python3 -m json.tool)

## Report

Report: files created, test list with pass counts, e2e check outputs
(exit codes), deviations.
