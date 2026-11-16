"""Harness-integrity probes for suites/regression.json.

These checkers pin the measurement infrastructure itself, so they must never
reach for an absolute path baked into the manifest: a hardcoded
/Users/<someone>/git/meept-bench/... is silently a no-op on every other
checkout (the file is absent, the shell test short-circuits, the probe passes
without measuring anything).

Instead they read the two directories the runner exports to every checker
subprocess (internal/checkers/checkers.go exitZero):

    MEEPT_BENCH_TRANSCRIPT_DIR  THIS run's <outDir>/transcripts
    MEEPT_BENCH_RESULTS_DIR     THIS run's outDir, i.e. where results.jsonl is
                                being appended for this very run

Both are absolute: the runner resolves the out dir with filepath.Abs because
checkers execute with cwd = the per-task worktree, where a relative
"results/..." would land in a throwaway directory that is deleted afterwards.

Unset variable => hard failure. The previous fallback globbed
results/**/transcripts and took the newest mtime match, which let a stale run
dir from a dead daemon poison later gates (2026-09-26). Failing loudly is the
whole point: a probe that cannot see its own run must not report a verdict.

Invoked as:  python3 -I -B -c "<this file>" <probe-name>
"""

import fnmatch
import json
import os
import sys
from typing import NoReturn

# Each probe keeps the assertion it always had. The exit contract is the
# checker's verdict, so every failure path ends in exit 1 with a reason on
# stdout (the runner captures combined output as the check Detail).
PROBES = {}


def probe(name):
    def register(fn):
        PROBES[name] = fn
        return fn

    return register


def fail(message) -> NoReturn:
    """Print the reason and exit non-zero.

    Annotated NoReturn so the type checker knows every call site terminates:
    without it, a `fail(...)` inside an `except` block looks like it falls
    through to code using the unbound name.
    """
    print("HARNESS-INTEGRITY FAIL: " + message)
    sys.exit(1)


def _absolute_run_dir(var):
    """Read a runner-exported dir and return it as a canonical absolute path.

    Canonicalized with realpath because on macOS TMPDIR is a symlink
    (/var -> /private/var): a checker that compares the string form would
    reject a perfectly good directory whose symlinked spelling differs. The
    requirement is that the runner handed us an absolute path (so it means the
    same thing from inside the worktree cwd) — realpath keeps that guarantee
    while making the comparison work across symlink spellings.
    """
    d = os.environ.get(var, "")
    if not d:
        fail("%s is unset; refusing to read a global results/ dir" % var)
    if not os.path.isabs(d):
        fail("%s is relative (%r); runner must export an absolute dir" % (var, d))
    return os.path.realpath(d)


def results_dir():
    return _absolute_run_dir("MEEPT_BENCH_RESULTS_DIR")


def transcript_dir():
    return _absolute_run_dir("MEEPT_BENCH_TRANSCRIPT_DIR")


def one_transcript(pattern):
    """This run's newest transcript matching pattern, or fail closed.

    ``pattern`` is a glob (``*transcript-completeness*.json``), matched with
    fnmatch against the directory listing — the same semantics the original
    inline ``glob.glob(os.path.join(d, pattern))`` had, minus the ability to
    escape the transcript dir.

    Scoped to THIS run's transcript dir, so a transcript from an earlier,
    unrelated gate run can never satisfy the probe. An empty dir is a failure,
    not a pass: it means the runner wrote no transcript for this attempt.

    Returns a (path, transcript) tuple and never returns None — every error
    path exits, so the caller can unpack the result directly.
    """
    d = transcript_dir()
    try:
        entries = os.listdir(d)
    except OSError as exc:
        fail("cannot list transcript dir %s: %s" % (d, exc))
    # Restrict to regular .json files in THIS dir before glob-matching. Two
    # reasons: a subdirectory whose name happens to match must not be opened
    # as a transcript, and fnmatch's `*` also matches "/" — the original
    # glob.glob sat inside the transcript dir and could not escape it, so an
    # explicit name filter keeps the same scope instead of inheriting a
    # broader one from fnmatch's path semantics.
    names = sorted(
        (
            n
            for n in entries
            if n.endswith(".json")
            and os.path.isfile(os.path.join(d, n))
            and fnmatch.fnmatch(n, pattern)
        ),
        key=lambda n: os.path.getmtime(os.path.join(d, n)),
    )
    if not names:
        fail("no %s transcript in %s" % (pattern, d))
    # One attempt writes exactly one file per stem; newest mtime is the
    # deterministic pick if a stem is ever rewritten.
    path = os.path.join(d, names[-1])
    try:
        with open(path) as fh:
            transcript = json.load(fh)
    except (OSError, ValueError) as exc:
        fail("transcript %s unreadable: %s" % (path, exc))
    if not isinstance(transcript, dict):
        fail("transcript %s is %s, not a JSON object" % (path, type(transcript).__name__))
    return path, transcript


@probe("bus-trace-arrives")
def bus_trace_arrives():
    # a20b105c: the tool_trace only reaches the transcript if the bus
    # subscription that feeds it is wired. Empty trace == regression.
    path, t = one_transcript("*bus-trace-arrives*.json")
    trace = t.get("tool_trace") or []
    if len(trace) == 0:
        fail("tool_trace empty in %s - bus subscription regressed (a20b105c)" % path)
    print("OK bus-trace-arrives: tool_trace has %d call(s)" % len(trace))


@probe("transcript-completeness")
def transcript_completeness():
    # Every transcript must carry both ends of the exchange; a missing
    # final_reply or prompt means the runner recorded a half-turn.
    path, t = one_transcript("*transcript-completeness*.json")
    if not t.get("final_reply"):
        fail("final_reply missing/empty in %s" % path)
    if not t.get("prompt"):
        fail("prompt missing/empty in %s" % path)
    print("OK transcript-completeness: prompt + final_reply present")


@probe("harness-cost-row-present")
def harness_cost_row_present():
    # 01c4f38: a passing row must never carry a negative tokens_in. Negative
    # cost attribution would mean the usage reader mis-parsed a provider
    # response and the cost numbers in the scorecard are fiction.
    path = os.path.join(results_dir(), "results.jsonl")
    if not os.path.exists(path):
        # This run has not appended its first row yet (the checker runs inside
        # the first task). Nothing to check is not a failure.
        print("OK harness-cost-row-present: %s not written yet" % path)
        return
    rows = []
    with open(path) as fh:
        for lineno, line in enumerate(fh, 1):
            line = line.strip()
            if not line:
                continue
            try:
                rows.append((lineno, json.loads(line)))
            except ValueError as exc:
                fail("%s:%d is not JSON: %s" % (path, lineno, exc))
    bad = [
        (lineno, r.get("task_id"))
        for lineno, r in rows
        if r.get("verdict") == "pass" and r.get("tokens_in", 0) < 0
    ]
    if bad:
        fail("passing row(s) with negative tokens_in in %s: %s (01c4f38)"
             % (path, ", ".join("line %d task=%s" % (ln, tid) for ln, tid in bad)))
    print("OK harness-cost-row-present: %d row(s), none with negative tokens_in" % len(rows))


def main():
    if len(sys.argv) != 2 or sys.argv[1] not in PROBES:
        fail("usage: probe_check.py <%s>" % "|".join(sorted(PROBES)))
    PROBES[sys.argv[1]]()


if __name__ == "__main__":
    main()