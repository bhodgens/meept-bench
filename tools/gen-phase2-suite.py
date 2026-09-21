#!/usr/bin/env python3
"""Generate suites/campaign-phase2.json from the local replay-gold corpus.

The corpus ( meept repo: tools/classifier-eval/replay-gold.local.json5 )
carries verbatim user-transcript text, so it must never be committed here.
This script reads it at generation time and freezes it into a bench suite
manifest with routing expectations and intent-scaled timeouts.

Timeout policy (bench timeout policy, phase 2):
  - multi-step orchestration intents (quickplan, plan): 600s — those turns
    legitimately run 4-9 minutes on the local 8B chain (one phase-2 turn
    finished at ~540s under the old flat 240s policy).
  - every other intent (single-step: chat/code/review/git/analyze/debug/
    platform): 300s — completed phase-2 turns peaked at ~145s wall.

Usage:
  python3 tools/gen-phase2-suite.py \
      --corpus /Users/caimlas/git/meept/tools/classifier-eval/replay-gold.local.json5 \
      --out suites/campaign-phase2.json
"""
import argparse
import json
import re
import sys

MULTI_STEP_INTENTS = {"quickplan", "plan"}
MULTI_STEP_TIMEOUT = 600
SINGLE_STEP_TIMEOUT = 300

EXPECTED_AGENT = {
    "quickplan": "orchestrator",
    "plan": "planner",
    "code": "coder",
    "review": "coder",
    "git": "committer",
    "analyze": "analyst",
    "debug": "debugger",
    "platform": "chat",
}


def load_corpus(path):
    """Parse the JSON5 corpus with stdlib only (unquoted `cases:` key and
    trailing commas are the only JSON5 features the file uses)."""
    text = open(path, encoding="utf-8").read()
    body = text[text.index("{"):]
    body = re.sub(r'(?m)^(\s*)cases\s*:', r'\1"cases":', body)
    body = re.sub(r",(\s*[}\]])", r"\1", body)
    data = json.loads(body)
    cases = data.get("cases")
    if not isinstance(cases, list) or not cases:
        sys.exit("corpus %s: no cases array" % path)
    return cases


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--corpus", required=True,
                    help="path to replay-gold.local.json5 (untracked, stays local)")
    ap.add_argument("--out", required=True, help="suite manifest to write")
    args = ap.parse_args()

    cases = load_corpus(args.corpus)
    tasks = []
    for i, c in enumerate(cases):
        intent = c["expected_intent"]
        agent = c["expected_agent"] or EXPECTED_AGENT.get(intent, "")
        if intent not in EXPECTED_AGENT:
            sys.exit("case %d: unknown intent %r" % (i, intent))
        timeout = MULTI_STEP_TIMEOUT if intent in MULTI_STEP_INTENTS else SINGLE_STEP_TIMEOUT
        tasks.append({
            "id": "replay-%02d" % i,
            "prompt": c["input"],
            "timeout_seconds": timeout,
            "seeds": [1],
            "tags": ["replay", "phase2", intent],
            "expect_agent": agent,
            "expect_intent": intent,
            "checkers": [{"type": "exit_zero", "command": ["true"]}],
        })

    manifest = {
        "suite": "campaign-phase2",
        "description": ("Phase-2 live chain acceptance: %d frozen replay cases, "
                        "routing-only; timeouts scale with intent class "
                        "(%ds multi-step quickplan/plan, %ds everything else)"
                        % (len(tasks), MULTI_STEP_TIMEOUT, SINGLE_STEP_TIMEOUT)),
        "internal": True,
        "tasks": tasks,
    }
    with open(args.out, "w", encoding="utf-8") as f:
        json.dump(manifest, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print("wrote %s: %d tasks (%d multi-step @ %ds, %d single-step @ %ds)" % (
        args.out, len(tasks),
        sum(1 for t in tasks if t["timeout_seconds"] == MULTI_STEP_TIMEOUT),
        MULTI_STEP_TIMEOUT,
        sum(1 for t in tasks if t["timeout_seconds"] == SINGLE_STEP_TIMEOUT),
        SINGLE_STEP_TIMEOUT))


if __name__ == "__main__":
    main()
