#!/usr/bin/env bash
# update-baseline.sh <results.jsonl>
#
# Promote a GREEN run's results.jsonl to the baseline on the `bench-baselines`
# branch as baselines/<suite>.jsonl. NEVER let a red run update the baseline:
# the green check below refuses any row whose verdict is not pass|error-pass,
# i.e. anything other than "pass" (see VERDICT_TOLERATED).
#
# Requires: ORIGIN_REMOTE (default "origin") pointing at the repo. The script
# pushes directly to ORIGIN_REMOTE (used as the bare store in local testing);
# no working checkout is disturbed. A git worktree on a temp dir is used to
# stage the commit, per the leaf spec.
#
# Env overrides:
#   ORIGIN_REMOTE       git remote to push to          (default: origin)
#   BRANCH              baseline branch                (default: bench-baselines)
#   VERDICT_TOLERATED   extra verdicts allowed besides "pass" (space-sep)
#
# Exit codes: 0 baseline updated, 1 red run / not updated, 2 usage/other error.
set -euo pipefail

if [ $# -ne 1 ]; then
  echo "usage: update-baseline.sh <results.jsonl>" >&2
  exit 2
fi

SRC=$1
ORIGIN=${ORIGIN_REMOTE:-origin}
BRANCH=${BRANCH:-bench-baselines}
TOLERATED=${VERDICT_TOLERATED:-}

[ -f "$SRC" ] || { echo "update-baseline: not a file: $SRC" >&2; exit 2; }
[ -s "$SRC" ] || { echo "update-baseline: refusing empty results file: $SRC" >&2; exit 1; }

# --- green gate: every row must have a tolerated verdict --------------------
rejected=0
total=0
while IFS= read -r line; do
  [ -z "$line" ] && continue
  total=$((total + 1))
  verdict=$(printf '%s' "$line" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("verdict",""))' 2>/dev/null || true)
  ok=false
  case "$verdict" in
    pass) ok=true ;;
  esac
  for v in $TOLERATED; do
    [ "$verdict" = "$v" ] && ok=true
  done
  if [ "$ok" != true ]; then
    echo "update-baseline: RED row (verdict=$verdict) — refusing to update baseline" >&2
    rejected=$((rejected + 1))
  fi
done < "$SRC"

if [ "$rejected" -gt 0 ]; then
  echo "update-baseline: $rejected/$total rows not green — baseline NOT updated" >&2
  exit 1
fi
echo "update-baseline: green check passed ($total/$total rows pass)"

# --- suite name: from the first row's "suite" field --------------------------
suite=$(head -1 "$SRC" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("suite",""))' 2>/dev/null || true)
if [ -z "$suite" ]; then
  echo "update-baseline: could not read suite name from first row of $SRC" >&2
  exit 2
fi
case "$suite" in *[!A-Za-z0-9._-]*) echo "update-baseline: unsafe suite name: $suite" >&2; exit 2 ;; esac

# --- stage the commit in a throwaway worktree --------------------------------
scratch=$(mktemp -d)
cleanup() { git worktree remove --force "$scratch" >/dev/null 2>&1 || rm -rf "$scratch"; }
trap cleanup EXIT

current_head=$(git rev-parse HEAD)
if ! git fetch --quiet "$ORIGIN" "$BRANCH" 2>/dev/null; then
  echo "update-baseline: no existing $BRANCH on $ORIGIN — creating it from HEAD"
else
  current_head=$(git rev-parse --quiet --verify "FETCH_HEAD" || git rev-parse HEAD)
fi

git worktree add --detach "$scratch" "$current_head" >/dev/null 2>&1 \
  || { echo "update-baseline: failed to create worktree" >&2; exit 2; }

mkdir -p "$scratch/baselines"
install -m 0644 "$SRC" "$scratch/baselines/$suite.jsonl"

git -C "$scratch" add "baselines/$suite.jsonl"
if git -C "$scratch" diff --cached --quiet; then
  echo "update-baseline: baseline unchanged; nothing to commit"
  exit 0
fi

git -C "$scratch" \
  -c user.name='github-actions[bot]' \
  -c user.email='41898282+github-actions[bot]@users.noreply.github.com' \
  commit --quiet -m "baseline: $suite ($(date -u +%Y-%m-%dT%H:%M:%SZ), $total green rows)

Auto-updated by scripts/update-baseline.sh from a green CI run."

echo "update-baseline: force-pushing $BRANCH -> $ORIGIN"
git -C "$scratch" push --force "$ORIGIN" "HEAD:refs/heads/$BRANCH"

echo "update-baseline: baseline updated: $ORIGIN/$BRANCH:baselines/$suite.jsonl"
