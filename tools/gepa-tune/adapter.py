#!/usr/bin/env python3
"""GEPA adapter for meept's LLM intent classifier (dual-component setup).

Components optimized jointly:
  instruction = the 27-lane description block (llm_classifier.go descriptions map)
  examples    = the few-shot Examples block (llm_classifier.go:850-855)

Task model  = the REAL classifier runtime: llama.cpp LFM2.5-8B on
              127.0.0.1:8080 (no auth). Reflection = flashnext via the
              socrates LiteLLM proxy.

Regression guard: harvested-lane cases the production seed gets right are
PROTECTED and score PROTECT_WEIGHT when correct, so losing one dominates any
net gain; the run additionally prints an explicit zero-regression verdict.

Usage:
  .venv/bin/python adapter.py --selftest   # offline stub, no network
  .venv/bin/python adapter.py --probe      # real models, few cases
  .venv/bin/python adapter.py --run        # full optimization run
Env overrides: MEEPT_TUNE_URL / MEEPT_TUNE_MODEL / MEEPT_REFLECT_URL / MEEPT_REFLECT_MODEL
"""
import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request

GEPA_DIR = os.path.dirname(os.path.abspath(__file__))
MEEPT = os.path.abspath(os.path.join(GEPA_DIR, "..", "..", "..", "meept"))
CORPUS_MAIN = os.path.join(MEEPT, "testdata", "eval", "classifier-test-corpus.json5")
CORPUS_ADV = os.path.join(MEEPT, "testdata", "eval", "classifier-adversarial-corpus.json5")
CORPUS_LANES = os.path.join(GEPA_DIR, "gepa-lanes-corpus.json5")

TASK_URL = os.environ.get("MEEPT_TUNE_URL", "http://127.0.0.1:8080/v1")
TASK_MODEL = os.environ.get(
    "MEEPT_TUNE_MODEL",
    "/Volumes/LLMs/LiquidAI/LFM2.5-8B-A1B-GGUF/LFM2.5-8B-A1B-Q4_K_M.gguf",
)
REFLECT_URL = os.environ.get("MEEPT_REFLECT_URL", "http://socrates:4000/v1")
REFLECT_MODEL = os.environ.get("MEEPT_REFLECT_MODEL", "flashnext")

PROMPT_TEMPLATE = """Classify this user input:

Input: {{INPUT}}

Available intents:
{{INSTRUCTION}}

Examples:
{{EXAMPLES}}

Return JSON with intent, confidence, and reasoning."""

# fallback if Go-source extraction ever fails
PROD_EXAMPLES_FALLBACK = (
    '- "create a file named X then tell me the path" -> code (the report-back is part of the work, not a second intent)\n'
    '- "implement the plan tasks in order" -> quickplan\n'
    '- "did the change get made?" -> recall\n'
    '- "what files did you make?" -> recall'
)


def _api_key_for(url):
    """socrates proxy needs the LiteLLM key; loopback llama.cpp does not.
    Key is read from ~/.hermes/.env and never logged."""
    if "socrates" in url or "4000" in url:
        env = os.environ.get("LITELLM_API_KEY")
        if env:
            return env
        envpath = os.path.expanduser("~/.hermes/.env")
        if os.path.exists(envpath):
            for line in open(envpath):
                if line.startswith("LITELLM_API_KEY="):
                    return line.strip().split("=", 1)[1]
    return ""


def _post(url, payload, timeout_s, attempts=3):
    headers = {"Content-Type": "application/json"}
    key = _api_key_for(url)
    if key:
        headers["Authorization"] = f"Bearer {key}"
    req = urllib.request.Request(url + "/chat/completions",
                                 data=json.dumps(payload).encode(), headers=headers)
    last_err = None
    for i in range(attempts):
        try:
            with urllib.request.urlopen(req, timeout=timeout_s) as resp:
                return json.loads(resp.read())
        except (TimeoutError, urllib.error.URLError) as e:
            last_err = e
            print(f"  {url} attempt {i+1}/{attempts} failed: {e}", file=sys.stderr)
            time.sleep(2 * (i + 1))
    raise last_err


def parse_json5(text):
    text = re.sub(r"/\*.*?\*/", "", text, flags=re.S)
    text = re.sub(r"(?m)^(\s*)//[^\n]*", r"\1", text)
    text = re.sub(r"([,}\]])\s+//[^\n]*", r"\1", text)
    text = re.sub(r"([{,]\s*)([A-Za-z_][\w]*)(\s*:)", r'\1"\2"\3', text)
    text = re.sub(r",(\s*[}\]])", r"\1", text)
    return json.loads(text, strict=False)


def load_corpus():
    cases = []
    for path in (CORPUS_MAIN, CORPUS_ADV, CORPUS_LANES):
        harvested = path != CORPUS_LANES
        data = parse_json5(open(path).read())
        if "categories" in data:
            for items in data["categories"].values():
                for it in items:
                    cases.append({"input": it["input"], "intent": it["expected_intent"],
                                  "ood": bool(it.get("ood", False)), "harvested": harvested})
        elif "cases" in data:
            for it in data["cases"]:
                if not it.get("expected_intent"):
                    continue
                cases.append({"input": it["input"], "intent": it["expected_intent"],
                              "ood": bool(it.get("ood", False)), "harvested": harvested})
    return cases


def stratified_split(cases, train_n=160, val_n=200, seed=7):
    """Per-lane round-robin so every lane appears in BOTH splits."""
    import random
    rng = random.Random(seed)
    by_lane = {}
    for c in cases:
        by_lane.setdefault(c["intent"], []).append(c)
    for v in by_lane.values():
        rng.shuffle(v)
    train, val = [], []
    lanes = sorted(by_lane)
    idx = {l: 0 for l in lanes}
    while len(train) < train_n or len(val) < val_n:
        progressed = False
        for l in lanes:
            if idx[l] < len(by_lane[l]):
                progressed = True
                c = by_lane[l][idx[l]]
                idx[l] += 1
                if len(val) < val_n:
                    val.append(c)
                elif len(train) < train_n:
                    train.append(c)
        if not progressed:
            break
    return train, val


def render(inp, candidate):
    return (PROMPT_TEMPLATE
            .replace("{{INPUT}}", inp)
            .replace("{{INSTRUCTION}}", candidate["instruction"])
            .replace("{{EXAMPLES}}", candidate["examples"]))


def classify(inp, candidate, timeout_s=45):
    """One task-model call; returns the full parsed JSON (intent,
    confidence, reasoning) so reflection gets the model's own words."""
    data = _post(TASK_URL, {
        "model": TASK_MODEL,
        "temperature": 0,
        "messages": [{"role": "user", "content": render(inp, candidate)}],
    }, timeout_s)
    content = data["choices"][0]["message"]["content"]
    m = re.search(r"\{.*\}", content, re.S)
    if not m:
        return {"intent": None, "confidence": None, "reasoning": content[:300]}
    try:
        j = json.loads(m.group(0), strict=False)
        return {"intent": j.get("intent"), "confidence": j.get("confidence"),
                "reasoning": str(j.get("reasoning", ""))[:400]}
    except (json.JSONDecodeError, AttributeError):
        return {"intent": None, "confidence": None, "reasoning": content[:300]}


def reflect(prompt_text, timeout_s=240):
    data = _post(REFLECT_URL, {
        "model": REFLECT_MODEL,
        "messages": [{"role": "user", "content": prompt_text}],
    }, timeout_s, attempts=2)
    return data["choices"][0]["message"]["content"]


def real_lane_block():
    src = open(os.path.join(MEEPT, "internal", "agent", "llm_classifier.go")).read()
    m = re.search(r"descriptions := map\[string\]string\{(.*?)\n\t\}", src, re.S)
    if not m:
        raise RuntimeError("could not locate descriptions map")
    lines = []
    for line in m.group(1).splitlines():
        lm = re.match(r'\s*string\((\w+)\):\s*"(.*)",?\s*$', line)
        if lm:
            lines.append(f"- {lm.group(1).replace('Intent', '').lower()}: {lm.group(2)}")
    return "\n".join(lines)


def real_examples_block():
    """Extract the production few-shot Examples raw-string block."""
    try:
        src = open(os.path.join(MEEPT, "internal", "agent", "llm_classifier.go")).read()
        m = re.search(r'Examples:\\n"\)\s*\n\s*sb\.WriteString\(`(.*?)`\)', src, re.S)
        if m:
            block = m.group(1).strip()
            if block:
                return block
    except Exception:
        pass
    return PROD_EXAMPLES_FALLBACK


def seed_candidate():
    return {"instruction": real_lane_block(), "examples": real_examples_block()}


PROTECTED_CACHE = os.path.join(GEPA_DIR, "protected_cases_v3.json")
PROTECT_WEIGHT = 50.0


def compute_protected(val, seed_cand, task_fn=None):
    """Harvested-lane val cases the seed candidate gets right."""
    if task_fn is None:
        task_fn = classify
    return [c["input"] for c in val
            if c.get("harvested") and task_fn(c["input"], seed_cand)["intent"] == c["intent"]]


class MeeptAdapter:
    """GEPA adapter over two components: instruction + examples."""

    propose_new_texts = None  # gepa 0.1.4 probe needs the attr to exist

    COMPONENT_TASKS = {
        "instruction": (
            "Rewrite the LANE DESCRIPTION block (one '- lane: description' line "
            "per lane, every lane exactly once). Diagnose WHY each miss happened "
            "using the model's own reasoning. Keep total size similar (the daemon "
            "pins prompt size). Put the new block in ``` blocks."
        ),
        "examples": (
            "Rewrite the FEW-SHOT EXAMPLES block: lines of the form "
            '"short input" -> lane (why in a few words). Use it to anchor lanes '
            "the model confuses: add contrasting pairs (e.g. commit -> git vs "
            "what did I tell you -> recall vs use the X tool -> tooluse). "
            "Keep 6-16 lines total (the daemon pins prompt size); preserve or "
            "improve the existing anchors. Put the new block in ``` blocks."
        ),
    }

    def __init__(self, train, val, task_fn=classify, protected_inputs=None):
        self.train = train
        self.val = val
        self.task_fn = task_fn
        self.protected = set(protected_inputs or ())

    def evaluate(self, batch, candidate, capture_traces=False, **kwargs):
        from gepa.core.adapter import EvaluationBatch
        outputs, scores, trajs = [], [], []
        for case in batch:
            res = self.task_fn(case["input"], candidate)
            pred, conf, reason = res["intent"], res.get("confidence"), res.get("reasoning", "")
            correct = pred == case["intent"]
            is_prot = case["input"] in self.protected
            score = (1.0 if correct else 0.0) * (PROTECT_WEIGHT if is_prot else 1.0)
            scores.append(score)
            outputs.append(res)
            trajs.append({
                "score": score, "example": case, "pred": pred,
                "feedback": (
                    f"input: {case['input'][:300]!r}\n"
                    f"expected: {case['intent']} | predicted: {pred} | confidence: {conf}\n"
                    f"model's own reasoning: {reason}\n"
                    f"verdict: {'correct' if correct else 'WRONG'}"
                    + ("\nNOTE: PROTECTED case - production already handles it; your change "
                       "must not lose it." if is_prot else "")
                ),
            })
        return EvaluationBatch(outputs=outputs, scores=scores,
                               trajectories=trajs if capture_traces else None)

    def make_reflective_dataset(self, candidate, eval_batch, components_to_update, **kwargs):
        trajs = eval_batch.trajectories or []
        rows = [{
            "Inputs": t["example"]["input"][:200],
            "Expected": t["example"]["intent"],
            "Predicted": t["pred"],
            "ModelReasoning": t["feedback"].split("model's own reasoning: ")[-1].split("\n")[0],
        } for t in trajs if t["score"] == 0.0][:20]
        out = {}
        for name in components_to_update:
            if not rows:
                out[name] = [{"Guidance": "All cases correct. Keep this component unchanged."}]
            else:
                out[name] = [{"Misclassified cases": rows, "Task": self.COMPONENT_TASKS[name]}]
        return out


def eval_candidate(candidate, cases, task_fn=None):
    if task_fn is None:
        task_fn = classify
    hit = 0
    for c in cases:
        try:
            hit += task_fn(c["input"], candidate)["intent"] == c["intent"]
        except Exception:
            pass
    return hit / len(cases)


# ---------- offline selftest ----------

STUB_RULES = {
    "code": ("create", "write a ", "implement", "function", "file", "component", "script"),
    "recall": ("did ", "what files", "remember", "past "),
    "chat": ("hello", "hi", "thanks"),
}


def stub_chat(inp, candidate):
    low = inp.lower()
    for intent, cues in STUB_RULES.items():
        if any(c in low for c in cues):
            return {"intent": intent, "confidence": 0.5, "reasoning": "stub"}
    text = candidate["instruction"] + "\n" + candidate["examples"]
    if "remind" in text and ("remind" in low or "every day" in low or "tomorrow" in low):
        return {"intent": "schedule", "confidence": 0.5, "reasoning": "stub cue"}
    return {"intent": "chat", "confidence": 0.2, "reasoning": "stub default"}


def run_selftest():
    cases = load_corpus()
    print(f"corpus: {len(cases)} cases, {len({c['intent'] for c in cases})} lanes")
    train, val = stratified_split(cases, 40, 20)
    lanes = sorted({c["intent"] for c in cases})
    seed_cand = {"instruction": "\n".join(f"- {l}: (description)" for l in lanes),
                 "examples": '- "hello" -> chat'}
    import gepa
    result = gepa.optimize(
        seed_candidate=seed_cand,
        trainset=train, valset=val,
        adapter=MeeptAdapter(train, val, task_fn=stub_chat),
        reflection_lm=lambda msgs: ("Add cue words 'remind', 'every day', 'tomorrow' "
                                    "to the schedule lane description."),
        max_metric_calls=200, display_progress_bar=False,
    )
    best = result.best_candidate
    assert "instruction" in best and "examples" in best
    print("SELFTEST OK (2 components active)")


def run_real(max_calls=1200):
    import gepa
    cases = load_corpus()
    print(f"corpus {len(cases)} cases / {len({c['intent'] for c in cases})} lanes")
    train, val = stratified_split(cases, 160, 200)
    print(f"train {len(train)}, val {len(val)}")
    seed_cand = seed_candidate()

    if os.path.exists(PROTECTED_CACHE):
        protected = json.load(open(PROTECTED_CACHE))
        print(f"loaded {len(protected)} protected cases from cache")
    else:
        protected = compute_protected(val, seed_cand)
        json.dump(protected, open(PROTECTED_CACHE, "w"))
        print(f"computed {len(protected)} protected cases ({len(protected)/max(1,len(val)):.0%} of val)")

    adapter = MeeptAdapter(train, val, protected_inputs=protected)
    result = gepa.optimize(
        seed_candidate=seed_cand,
        trainset=train, valset=val,
        adapter=adapter,
        reflection_lm=lambda msgs: reflect(
            "\n".join(m.get("content", "") if isinstance(m, dict) else str(m) for m in msgs)[:12000]),
        max_metric_calls=max_calls, display_progress_bar=False,
    )
    best = result.best_candidate
    json.dump(best, open(os.path.join(GEPA_DIR, "best_candidate.json"), "w"), indent=1)
    open(os.path.join(GEPA_DIR, "best_examples.txt"), "w").write(best["examples"])
    open(os.path.join(GEPA_DIR, "best_instruction.txt"), "w").write(best["instruction"])
    print("\n=== direct raw-accuracy re-evaluation on the SAME val split ===")
    seed_acc = eval_candidate(seed_cand, val)
    best_acc = eval_candidate(best, val)
    print(f"seed(prod blocks): {seed_acc*100:.1f}% on {len(val)} val cases")
    print(f"GEPA best:         {best_acc*100:.1f}% on {len(val)} val cases")
    lost = sum(1 for c in val if c["input"] in set(protected)
               and classify(c["input"], best)["intent"] != c["intent"])
    print(f"protected cases lost by best: {lost} "
          f"{'-> NOT LANDABLE' if lost else '-> zero-regression, candidate OK for review'}")
    instr_changed = best["instruction"] != seed_cand["instruction"]
    ex_changed = best["examples"] != seed_cand["examples"]
    print(f"changed components: instruction={instr_changed} examples={ex_changed}")


def run_probe(n=3):
    cand = seed_candidate()
    print(f"seed examples block ({len(cand['examples'].splitlines())} lines):")
    print(cand["examples"][:200])
    for c in load_corpus()[:n]:
        t0 = time.time()
        r = classify(c["input"], cand)
        dt = time.time() - t0
        ok = "OK " if r["intent"] == c["intent"] else "MISS"
        print(f"{ok} task {dt:4.1f}s -> {r['intent']} (want {c['intent']}) conf={r.get('confidence')}")
    t0 = time.time()
    out = reflect("Reply with OK and nothing else.")
    print(f"reflection {time.time()-t0:.1f}s -> {out[:60]!r}")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--selftest", action="store_true")
    ap.add_argument("--probe", action="store_true")
    ap.add_argument("--run", action="store_true")
    a = ap.parse_args()
    if a.selftest:
        run_selftest()
    elif a.probe:
        run_probe()
    elif a.run:
        run_real()
    else:
        print(__doc__)
