# GEPA prompt-tuning findings — meept intent classifier

Date: 2026-11-11. Tooling: `tools/gepa-tune/` (adapter.py, gepa 0.1.4 in
`.venv`, corpora loader, regression guard, dual-model setup).
Reference: github.com/gepa-ai/gepa (arXiv:2507.19457).

## Setup

- Task model: the REAL classifier runtime — llama.cpp serving
  LFM2.5-8B-A1B-Q4 (`local/lfm-8b-q4`, 127.0.0.1:8080, no auth).
- Reflection model: `flashnext` via the socrates LiteLLM proxy
  (GEPA paper's recipe: reflect with a stronger model than the task model).
- Prompt under test: `buildClassificationPrompt` in
  `meept/internal/agent/llm_classifier.go` — the 27-lane descriptions block
  and (in run 3) the few-shot `Examples:` block, extracted live from source.
- Metric: exact-lane accuracy on a stratified split of the labeled corpora:
  `meept/testdata/eval/classifier-test-corpus.json5` +
  `classifier-adversarial-corpus.json5` (harvested, trusted) and
  `gepa-lanes-corpus.json5` (112 cases authored 2026-11-11 to cover the 14
  production lanes absent from the eval corpora — treat those labels as
  authored guesses, not adjudicated gold).
  Split: 160 train / 200 val, round-robin per lane, all 27 lanes in both.
- Regression guard: harvested-lane val cases the production seed classifies
  correctly are PROTECTED (49 of 200); losing one costs 50 points in
  selection, and the final report re-checks lost-protected count explicitly
  with a NOT-LANDABLE verdict on any loss.

## Runs

1. Unguarded, descriptions only, task=flashnext (wrong model — superseded):
   seed 0.45 / best 0.483 on 120. +3.3 pts came with 3 regressions in
   git / recall / explore. Rejected for landing.
2. Guarded, descriptions only, task=8B, val=200: seed 46.0%, GEPA best
   46.0%, best == production block byte-identical. All 10 reflection
   proposals lost once regressions were priced in.
3. Guarded, descriptions + few-shot examples jointly, task=8B, val=200:
   seed 46.0%, best 46.0%, `changed components: instruction=False
   examples=False`. 7 real candidates proposed (rewritten git line, new
   contrasting example anchors) — none beat the seed.

## Conclusion

The production classifier prompt is a local optimum for the 8B on both
prompt axes GEPA can move. The remaining ~54% error mass is model-capability,
not prompt-wording. Do not spend further prompt-tuning effort without new
model-side changes.

## Lane failure map (for the classifier SFT campaign)

Per-lane seed accuracy on the 8B (120-case val slice, run 1; 4-5 cases/lane
so read as directional):

| lane | seed acc | note |
|---|---|---|
| clarify | 0/5 | never fires — "deploy it to the usual place" -> chat/code |
| platform | 0/4 | questions about the system get routed as code/chat |
| skeptic | 0/4 | adversarial-review asks not recognized |
| librarian | 0/5 | curation phrasing maps to plan/code |
| imageid | 0/5 | (harvested 13 excluded these; all authored-lane) |
| instruction | 1/5 | standing-rule phrasing mostly -> schedule |
| chat | 1/5 | over-absorbs everything ambiguous |
| quickplan | 1/4 | known-hard; see lfm_classifier.go few-shot anchors |
| research | 1/4 | collapses into search |
| review | 1/4 | collapses into analyze/code |
| videogen | 1/4 | |
| healthy lanes | 4-5/x | code, debug, schedule, git, search, security, status, explore, tooluse, imagegen, write, report |

Action for SFT (lfm-combined-sft A/B campaign): add labeled training
examples for clarify, platform, skeptic, librarian, instruction first —
these are the lanes where the 8B has no decision boundary, not a fuzzy one.
Contrast pairs that reflection identified: commit->git vs what-did-I-tell-you->recall
vs use-the-X-tool->tooluse; summarize-done-work->report vs is-it-running->status.

## Reusing this tooling

- `.venv/bin/python adapter.py --selftest` — offline loop proof (no network).
- `.venv/bin/python adapter.py --probe` — 3-case live sanity on both models.
- `.venv/bin/python adapter.py --run` — full regression-guarded optimization.
  Takes ~15 min at 1200 metric calls on the 8B endpoint.
- After ANY change to llm_classifier.go descriptions/examples or a new
  classifier model build, `--run` doubles as the validation gate: a candidate
  that beats 46% with zero protected losses is evidence-worthy for landing;
  the seed-vs-best + lost-protected printout at the end of the log is the verdict.
- `protected_cases_v3.json` must be deleted when the seed blocks or the split
  change (it is keyed to both).
- gepa 0.1.4 pitfalls (fixed in adapter.py): EvaluationBatch return type;
  `propose_new_texts = None` class attribute required or reflection crashes;
  JSON5 corpus parse must not strip `//` inside strings and needs
  `strict=False`.

## Honest caveats

- 46% mixes harvested (trusted) and authored (guess) lanes. Harvested-only
  accuracy was ~54% on the same runs.
- Per-lane val counts are 4-8; the failure map is directional, not precise.
- GEPA here explored only 2 of several prompt surfaces (the multi-intent
  prompt and the keyword/prompt-router sidecar lanes were not touched).
