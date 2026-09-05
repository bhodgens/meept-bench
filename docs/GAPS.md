# Gaps meept Must Close Before Scores Are Competitive

Pre-registered expectations from the 2026-08-24 parity audit. Written BEFORE first results so we cannot rationalize failures later.

## Blocking gaps (benchmarks stall without these)

1. **Computer-use / browser tooling** — blocks GAIA L3, WebArena, OSWorld entirely. Path chosen: wire cua-driver MCP server (MIT, trycua/cua) into meept's existing MCP client config + author a bundled skill teaching the capture→click→verify loop; browser tasks additionally need a headless-Chromium MCP server or chromedp-based tool family.
2. **Document readers** — GAIA includes PDF/XLSX/DOCX attachments even in text-only splits unless filtered. Either filter harder for phase 2 or enable an office-doc MCP server.

## Score-suppressing gaps (runs work, scores suffer)

3. **Loop guards** — no repetition/no-progress/duplicate-query rollback today; long-horizon GAIA tasks punish this hardest. Borrow list item 5 in the audit report.
4. **Local-model tier** — $/task numbers will be uncompetitive vs atomic-agent's published economics until stable-prefix prompt ordering + a managed local small-model path exist. Affects cost columns, not pass rates.
5. **Steering UX over RPC** — benchmark runner drives the daemon headlessly; if steering/follow-up queues behave differently over RPC than TUI, multi-turn suites (τ-bench) will surface it. Verify early in phase 1.
   *Phase-1 check:* phase-1/2 tasks are single-turn (one chat request → final reply → checkers), so no steering defect surfaced — but the harness does not exercise steering/follow-up at all yet. Still open until a multi-turn suite runs.
6. **Approval gates with no human present** — τ-bench and any suite hitting `require_confirmation_high` actions will deadlock waiting on approvals that never come. Harness needs an auto-approver mode in the daemon config (or per-suite policy file) — and results must disclose it.
   *Phase-2 update:* the disclosure half landed — runner `--auto-approved` flag and a per-row `auto_approved` field (`internal/results/results.go`). Auto-approval of daemon confirmation gates during headless runs is still open; current smoke/regression tasks simply avoid confirmation-gated actions.

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
   **Status (2026-09-01): FIXED in meept** — `4f48e129` (classifier keywords)
   + `a0939721` (tool_hint→coder mapping). The smoke suite's three
   `expect_agent: coder` file-write tasks cover the prompt shape, and the
   regression suite pins it (`file-write-routes-coder`,
   `tool-hint-coder-maps-coder`, tagged with both commits — passing in the
   two local regression runs). Unblocks Terminal-Bench-style suites.
10. **Topic detector substring match misroutes threads** — "Cre*ate*"
    matches the food keyword "*eat*", tagging the thread
    `-thread-food-…`. Cosmetic today, but thread routing keys off it.
11. **Chat RPC has a hard server-side 120s proxy timeout** — long agent
    runs outlive the RPC reply path. The harness now falls back to the
    `chat_message` bus topic, but any RPC-only client hits the same wall.
12. **Session identity is split** — `project.set` needs the primary ID
    (`session-…`) while chat/session lookups key on the conversation ID
    (`conv-…`). Documented here so future adapters bind both correctly.
    *Phase-2 update:* the bench harness now binds both IDs
    (`session.create` → `project.set` → conv-ID chat, commit `a76414a`);
    pinned by regression tasks tagged `gap-12-session` (`session-workdir-bound`,
    `memory-store-marker`, `memory-recall-marker`).

## Steering over RPC (gap 5 closure — 2026-09-05)

P2.4 steering probe (suites/steering.json, 7 runs) findings:

1. **Harness complete**: chat.steer/chat.followup RPCs exist; runner
   dispatches turns (RPC → thread-queue → chat fallback), awaits turn
   completion via queue-quiet detection, records turn receipts.
2. **Daemon-side reliability gap**: thread-turn execution is
   environment-gated. With agnes quota parked loops, turns time out or
   complete without acting (midrun-correction: attempt 1 pass, attempt 2
   fail; control timeout at 420s). With quota headroom (run 9), turns
   execute with tools end-to-end (followup receipt written, checker green).
3. **Turn context**: steered turns need self-contained instructions —
   anaphoric corrections ("name IT result2.txt") resolve unreliably in
   the thread scope; explicit instructions ("Create result2.txt …
   file_write direct:true") work.
4. **Status**: steering suite tagged `steering`; not in the regression
   gate until daemon turn-execution is reliable under quota pressure.
   Re-verify after: agnes quota headroom OR deterministic local agent model.
