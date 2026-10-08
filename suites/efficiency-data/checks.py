"""Efficiency checker: does the transcript show the run took the DIRECT path?

Every other bench suite asserts correctness only — that meept produced the right
artifact. Nothing measures whether it got there CHEAPLY. That data is already
collected and simply unused: results.Transcript carries ToolTrace, Turns,
RoutedAgent, ClassificationMethod, StartedAt and EndedAt, and the runner writes
the transcript BEFORE running checkers (runner.go writeTranscript at :395,
checkers.Run at :407), so a checker can read it.

Usage (one invocation per check, from a suite manifest):

    MEEPT_BENCH_TRANSCRIPT_DIR  THIS run's transcripts/ directory. Supplied by
                                the runner via checkers.exitZero. Never glob
                                results/** — that leaks stale runs across gates.
    argv[1]                     transcript basename WITHOUT the .json suffix,
                                i.e. "<suite>-<task_id>-a<attempt>".
    argv[2:]                    bound name/value pairs, "name=value".

Bounds are UPPER bounds on cost. Exit 0 = within budget, exit 1 = over.

WHY THESE BOUNDS (measured, not guessed)
----------------------------------------
Across 150 archived transcripts the tool_trace length is bimodal: the mode is
0-2 calls, with a sparse tail at 7-19. That tail is not agent flailing — it is
the retry/dead-letter path (a failed step, a re-claim, a second attempt at the
same write). Per-task, the direct path is stable:

    write-answer-file          2,2,2,2      (one list + one write)
    tool-hint-coder-maps-coder 2,2,2,2
    pending-write-direct       2,2,2,2
    file-write-routes-coder    0,0,1,1,1,1,10

So a capability that needs N direct calls gets N+1 headroom: enough to absorb a
single legitimate extra probe (a directory listing before a write), far below the
tail where a regression that re-plans or re-tries would land. A budget of 999
would prove nothing; these are tuned to the observed mode.
"""
import json
import os
import pathlib
import sys


def fail(message):
    print("EFFICIENCY FAIL: " + message)
    sys.exit(1)


def transcript_path():
    """Resolve THIS run's transcript for the current attempt.

    The runner names the worktree AND the transcript file identically:
    runner.go RunTask builds `name = "<suite>-<task_id>-a<attempt>"`, passes it
    to mgr.Create (so it is the worktree basename) and to writeTranscript. The
    checker runs with cwd = that worktree, so pathlib.Path.cwd().name IS the
    stem — the same derivation suites/routing-repair-data/checks.py uses, and the
    reason no task_id field is needed in the manifest.

    Reading is pinned to MEEPT_BENCH_TRANSCRIPT_DIR (this run's transcripts/),
    never a glob over results/ — globbing leaks a stale run into a later gate
    (hit 2026-09-26 when gate-pre-push outlived its daemon). The transcript's own
    suite/task_id/attempt are then re-checked against the stem, so a mismatch
    fails loudly instead of silently measuring another run.
    """
    tdir = os.environ.get("MEEPT_BENCH_TRANSCRIPT_DIR")
    if not tdir:
        fail("MEEPT_BENCH_TRANSCRIPT_DIR is unset; refusing to read a global results/ dir")
    stem = pathlib.Path.cwd().name
    if not stem:
        fail("cannot derive the transcript stem from the worktree path")
    path = pathlib.Path(str(tdir)) / (stem + ".json")
    if not path.exists():
        fail("no transcript %s for this run's worktree %r" % (path, stem))
    try:
        tr = json.loads(path.read_text())
    except Exception as exc:  # noqa: BLE001 - surface the reason, fail closed
        fail("transcript %s unreadable: %s" % (path, exc))
    # Identity check: the stem is "<suite>-<task_id>-a<attempt>"; confirm the
    # transcript really is that run and not a neighbour's.
    if tr.get("attempt") is None or not stem.endswith("-a%d" % tr["attempt"]):
        fail("transcript %s does not match worktree stem %r (attempt drift)" % (path, stem))
    prefix = stem[: -len("-a%d" % tr["attempt"])]
    suite_name, _, task_id = prefix.partition("-")
    if tr.get("suite") != suite_name or tr.get("task_id") != task_id:
        fail("transcript identity mismatch for %r: suite=%r task=%r" % (
            stem, tr.get("suite"), tr.get("task_id")))
    return path, tr


NUMERIC_BOUNDS = ("max_tools", "min_tools", "max_turns", "min_turns")
TEXT_BOUNDS = ("agent_is", "agent_not", "method_is", "method_not")


def parse_bounds(argv):
    """Split name=value pairs into numeric and textual bounds.

    Numeric bounds compare against a count; the four *_is / *_not bounds compare
    against a routing string, so they must NOT be forced through int() — an
    earlier version did that and rejected every agent_is= bound as malformed,
    which would have made the routing assertions silently unreachable.
    """
    bounds = {}
    for raw in argv:
        if "=" not in raw:
            fail("bound %r is not name=value" % raw)
        name, _, value = raw.partition("=")
        if name in NUMERIC_BOUNDS:
            try:
                bounds[name] = int(value)
            except ValueError:
                fail("bound %s takes an integer, got %r" % (name, value))
        elif name in TEXT_BOUNDS:
            if value == "":
                fail("bound %s was given an empty value" % name)
            bounds[name] = value
        else:
            fail("unknown bound %r (known: %s)" % (name, ", ".join(NUMERIC_BOUNDS + TEXT_BOUNDS)))
    return bounds


def main():
    path, tr = transcript_path()
    bounds = parse_bounds(sys.argv[1:])
    tools = tr.get("tool_trace") or []
    turns = tr.get("turns") or []
    agent = tr.get("routed_agent") or ""
    method = tr.get("classification_method") or ""

    # --- upper bounds on cost -------------------------------------------
    limit = bounds.get("max_tools")
    if limit is not None and len(tools) > limit:
        fail("tool_trace has %d calls, budget is %d (call topics: %s)" % (
            len(tools), limit, ", ".join(sorted({str(t.get("topic")) for t in tools}))[:120]))

    limit = bounds.get("max_turns")
    if limit is not None and len(turns) > limit:
        fail("transcript records %d turns, budget is %d" % (len(turns), limit))

    # --- routing assertions ---------------------------------------------
    want = bounds.get("agent_is")
    if want is not None and agent != want:
        fail("routed_agent = %r, expected %r" % (agent, want))

    avoid = bounds.get("agent_not")
    if avoid is not None and agent == avoid:
        fail("routed_agent = %r, which must NOT be used for this task" % agent)

    want_m = bounds.get("method_is")
    if want_m is not None and method != want_m:
        fail("classification_method = %r, expected %r" % (method, want_m))

    avoid_m = bounds.get("method_not")
    if avoid_m is not None and method == avoid_m:
        fail("classification_method = %r, which must NOT be used for this task" % method)

    # --- the task must actually have DONE something ----------------------
    # A run that burned zero tool calls and still "passed" a cost budget is not
    # efficient, it is inert. Requiring real work keeps the budget honest.
    if bounds.get("min_tools") is not None and len(tools) < bounds["min_tools"]:
        fail("tool_trace has %d calls, expected at least %d — the task did no work" % (
            len(tools), bounds["min_tools"]))

    print("OK efficiency: tools=%d turns=%d agent=%s method=%s" % (
        len(tools), len(turns), agent or "-", method or "-"))


if __name__ == "__main__":
    main()