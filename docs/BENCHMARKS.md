# Benchmark Suite Access & Licensing

Notes gathered 2026-08-24. Verify each suite's current terms before first run — access policies change.

## GAIA specifics (gated dataset — read before running)

GAIA (gaia-benchmark/GAIA on HuggingFace) is a GATED dataset: access requires accepting conditions, and the gate explicitly forbids resharing the validation or test set outside gated/private repos or in crawlable form. Consequences for this repo:

1. NEVER commit GAIA task content, answers, or derived task files to meept-bench. Adapters reference the HF dataset by revision hash; each runner fetches from HF under the user's own accepted-gate token.
2. Artifacts ARE publishable: per-task results (task id, verdict, transcript summary, cost), aggregate scorecards, and submissions to the official leaderboard (validation set) follow what other agents already publish (atomic-agent ships GAIA artifacts as release assets). Publish results + methodology; never the tasks.
3. The official leaderboard path: gaia-benchmark/leaderboard HF Space takes submissions against the test set with private answers. Validation-set scores are self-reportable; test-set scores require submission. Decide per release which we claim.
4. Record the HF revision hash of the dataset snapshot in every result row for reproducibility.

| Suite | Access | License/terms | Notes for meept-bench |
|-------|--------|---------------|----------------------|
| GAIA (HuggingFace gaia-benchmark/GAIA) | HF dataset, validation split public; test split requires leaderboard submission | CC BY-NC-SA-ish for data, per HF card — CHECK CARD AT RUN TIME | Validation subset only for published numbers. Text-only L1/L2 first. Images/PDFs in some tasks — filter to text-only for phase 2 or add doc-reader MCP servers. |
| Terminal-Bench (laude-institute/terminal-bench) | GitHub + task registry | Apache-2.0 harness per repo README (verify) | Docker-based tasks map cleanly onto meept's docker runtime backend. |
| τ-bench (sierra-research/tau-bench) | GitHub | Apache-2.0 per repo (verify); uses OpenAI/Anthropic APIs for user-simulator | User-simulator needs its own model config; record simulator model in results. |
| SWE-bench Lite (princeton-nlp/SWE-bench) | HF + Docker images on GHCR/Docker Hub | MIT code; instance data per HF card | Heaviest dependency chain (Docker images ~per-instance). Do subset of 50, seeded deterministically. |
| LongMemEval | HF (xiaowu0162/LongMemEval?) verify ID | research-permissive per paper/HF card (verify) | Memory-focused; directly tests episodic+epistemic tiers. Small split first. |
| LoCoMo | HF/adapted repos | research use (verify) | Alternative/complement to LongMemEval; conversation-grounded QA. |
| WebArena / WebArena-lite | self-hosted sites required | BSD-3 harness (verify) | Deferred until browser tooling exists in meept. |
| OSWorld | GitHub + VMs | Apache-2.0 (verify) | Deferred: computer-use gap. |

## Rules

1. Re-verify every license line at integration time; this file records intent, not clearance.
2. Never redistribute benchmark task content in this repo — store adapters + manifests referencing the upstream source.
3. Published scorecards must state: exact task list, seeds, model(s), date, and "self-run" labeling.
4. If a suite's terms forbid publishing scores (some do), run it internally for regression tracking only and mark it internal in the manifest.
