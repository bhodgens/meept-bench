#!/usr/bin/env python3
"""Deterministic local LLM judge for meept-bench llm_judge checkers.

Contract (identical to the --judge-cmd stdin/stdout contract):
  stdin:  "<rubric>\n---\n<answer>\n"
  stdout: "<score 0..1> <rationale>"  (single line; first token is the score)

Calls a local llama-server OpenAI-compatible chat endpoint with temperature 0
and a greedy instruction prompt, so repeated calls on the same input are as
deterministic as the server allows.

Environment:
  JUDGE_URL      server base URL (default http://127.0.0.1:8080)
  JUDGE_MODEL    model name to send (default "local"; llama-server ignores it
                 unless it serves multiple models)
  JUDGE_TIMEOUT  request timeout in seconds (default 120)

Usage as a judge command:
  meept-bench run --judge-cmd "python3 scripts/judge-local.py" ...
"""

import json
import os
import re
import sys
import urllib.error
import urllib.request

DEFAULT_URL = "http://127.0.0.1:8080"
GREEDY_PROMPT = 'Score strictly per rubric. Output ONLY "<score> <rationale>" on one line.'


def fail(msg: str) -> int:
    print(f"judge-local: {msg}", file=sys.stderr)
    return 1


def main() -> int:
    raw = sys.stdin.read()
    rubric, sep, answer = raw.partition("\n---\n")
    if not sep:
        return fail("stdin must be '<rubric>\\n---\\n<answer>'")

    base = os.environ.get("JUDGE_URL", DEFAULT_URL).rstrip("/")
    url = base + "/v1/chat/completions"
    body = {
        "model": os.environ.get("JUDGE_MODEL", "local"),
        "messages": [
            {"role": "system", "content": GREEDY_PROMPT},
            {"role": "user", "content": f"{rubric}\n---\n{answer}"},
        ],
        "temperature": 0,
        "max_tokens": 128,
        "stream": False,
    }
    req = urllib.request.Request(
        url,
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        timeout = float(os.environ.get("JUDGE_TIMEOUT", "120"))
    except ValueError:
        return fail("JUDGE_TIMEOUT must be a number")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read().decode())
    except urllib.error.HTTPError as exc:
        return fail(f"HTTP {exc.code} from {url}: {exc.read()[:500]!r}")
    except (urllib.error.URLError, TimeoutError, OSError, json.JSONDecodeError) as exc:
        return fail(f"request to {url} failed: {exc}")

    try:
        content = data["choices"][0]["message"]["content"]
    except (KeyError, IndexError, TypeError):
        return fail(f"unexpected response shape: {data!r}")

    # Single-line contract: collapse all whitespace runs, trim ends.
    line = re.sub(r"\s+", " ", str(content)).strip()
    if not line:
        return fail("empty judge completion")
    print(line)
    return 0


if __name__ == "__main__":
    sys.exit(main())
