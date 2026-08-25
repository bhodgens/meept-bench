# Gaps meept Must Close Before Scores Are Competitive

Pre-registered expectations from the 2026-08-24 parity audit. Written BEFORE first results so we cannot rationalize failures later.

## Blocking gaps (benchmarks stall without these)

1. **Computer-use / browser tooling** — blocks GAIA L3, WebArena, OSWorld entirely. Path chosen: wire cua-driver MCP server (MIT, trycua/cua) into meept's existing MCP client config + author a bundled skill teaching the capture→click→verify loop; browser tasks additionally need a headless-Chromium MCP server or chromedp-based tool family.
2. **Document readers** — GAIA includes PDF/XLSX/DOCX attachments even in text-only splits unless filtered. Either filter harder for phase 2 or enable an office-doc MCP server.

## Score-suppressing gaps (runs work, scores suffer)

3. **Loop guards** — no repetition/no-progress/duplicate-query rollback today; long-horizon GAIA tasks punish this hardest. Borrow list item 5 in the audit report.
4. **Local-model tier** — $/task numbers will be uncompetitive vs atomic-agent's published economics until stable-prefix prompt ordering + a managed local small-model path exist. Affects cost columns, not pass rates.
5. **Steering UX over RPC** — benchmark runner drives the daemon headlessly; if steering/follow-up queues behave differently over RPC than TUI, multi-turn suites (τ-bench) will surface it. Verify early in phase 1.
6. **Approval gates with no human present** — τ-bench and any suite hitting `require_confirmation_high` actions will deadlock waiting on approvals that never come. Harness needs an auto-approver mode in the daemon config (or per-suite policy file) — and results must disclose it.

## Measurement infrastructure gaps

7. **Deterministic tool variants** — web search/fetch results change daily. FrontierAgent ships eval-aligned deterministic tool variants; meept-bench needs at minimum: cached-fetch mode and a frozen-search fixture set, recorded in manifests.
8. **Cost attribution per subtask** — meept's token trickle-up gives task-level totals; scorecards want step-level breakdowns to explain WHERE cost goes. Analytics may already store this — verify before building anything.

## Non-goals

Do NOT close gap 6 by weakening security defaults globally; auto-approval is a suite-scoped config, disclosed per result row.

## Live smoke-run findings (2026-08-24, meept-bench v0.1)

Verified against a live daemon during phase-1 bring-up:

9. **Intent classifier hijacks task prompts** — "Create a file named
   answer.txt…" dispatches as `intent_type=skill confidence=0.747` and the
   chat agent answers with platform introspection (agent/tool listings)
   instead of performing the task. The word "create" appears to collide
   with skill keywords in the classifier. Blocks all Terminal-Bench-style
   suites until fixed. Reproduce: `meept-bench run --suite suites/smoke.json`.
10. **Topic detector substring match misroutes threads** — "Cre*ate*"
    matches the food keyword "*eat*", tagging the thread
    `-thread-food-…`. Cosmetic today, but thread routing keys off it.
11. **Chat RPC has a hard server-side 120s proxy timeout** — long agent
    runs outlive the RPC reply path. The harness now falls back to the
    `chat_message` bus topic, but any RPC-only client hits the same wall.
12. **Session identity is split** — `project.set` needs the primary ID
    (`session-…`) while chat/session lookups key on the conversation ID
    (`conv-…`). Documented here so future adapters bind both correctly.
