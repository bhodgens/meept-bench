"""Deterministic reply rules via the existing command-judge protocol; no LLM."""
import re
import sys


def grade(rule, answer):
    text = answer.strip().lower().replace("**", "").replace("`", "")
    if rule == "arithmetic":
        return bool(re.fullmatch(r"(?:(?:17\s*[*×x]\s*24\s*=\s*)|(?:the (?:answer|result|product) is\s+))?408[.!]?", text))
    if rule == "path":
        # Require the named operands, the faulty addition and multiplication fix.
        # This is a deliberately narrow semantic/lexical gate, not a general judge.
        if re.search(r"cannot|can't|unable|need (?:the|more)|might|maybe|not sure", text):
            return False
        return all(re.search(p, text) for p in (
            r"\bwidth\b", r"\bheight\b", r"\barea\b",
            r"width\s*\+\s*height|\badd(?:s|ing|ition)?\b|\bsum\b",
            r"width\s*\*\s*height|\bmultip(?:ly|lication|lying)\b|\bproduct\b",
            r"\b(?:bug|problem|incorrect|wrong|instead|needs?|should|rather|not)\b",
        ))
    return False


if __name__ == "__main__":
    rubric, sep, answer = sys.stdin.read().partition("\n---\n")
    rule = rubric.removeprefix("routing-repair:").strip()
    passed = bool(sep) and rubric.startswith("routing-repair:") and grade(rule, answer)
    print(f"{int(passed)} deterministic routing-repair {rule}: {'matched' if passed else 'not matched'}")
