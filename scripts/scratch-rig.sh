#!/usr/bin/env bash
# scratch-rig.sh — one-command isolated bench rig.
#
# Provisions a self-contained meept scratch rig (own MEEPT_HOME, own daemon,
# own runtime ports), waits for health, runs the bench against it with
# MEEPT_BENCH_SOCKET pointed at the rig socket, and tears EVERYTHING down on
# exit (trap EXIT). The user's live ~/.meept state is never touched: the
# daemon runs with MEEPT_HOME inside a mktemp dir, and the bench binary's
# isolation guard (internal/isolation) independently refuses the live socket.
#
# Usage:
#   scripts/scratch-rig.sh [--suite SUITE] [--task SUBSTR] [--no-runtime]
#                          [--keep-rig] [--doctor-only] [-- BENCH_RUN_ARGS...]
#
#   --suite SUITE    suite manifest to run       (default suites/smoke.json)
#   --task SUBSTR    task ID substring filter    (default: all tasks)
#   --no-runtime     skip the llama-server lifecycle block in models.json5 —
#                    daemon boots in seconds (no model spawn); doctor and
#                    guard verification work, chat tasks error on model calls
#   --doctor-only    provision, run `doctor`, tear down (no suite run)
#   --keep-rig       do NOT tear down on exit; prints the rig dir + a
#                    `scratch-rig.sh --down RIGDIR` teardown command
#   -- ARGS          extra args appended to `meept-bench run`
#
# Env overrides:
#   MEEPT_DAEMON_BIN  daemon binary    (default ~/git/meept/bin/meept-daemon)
#   MEEPT_BENCH_BIN   bench binary     (default: build ./cmd/meept-bench)
#   MEEPT_RIG_MODEL   GGUF for the rig runtime
#                     (default /Volumes/LLMs/LiquidAI/LFM2.5-8B-A1B-GGUF/
#                      LFM2.5-8B-A1B-Q4_K_M.gguf)
#   MEEPT_RIG_REPO    repo tasks fork worktrees from (default ~/git/meept)
#   MEEPT_RIG_AGENTS  agents dir to copy (default <meept-repo>/config/agents)
#
# Ports: the rig NEVER binds the live runtime ports on this machine
# (8080 chat, 8081 GUI TLS, 8082 prompt-router, 8084 extract, 8143 local
# classifier). HTTP and llama-server ports are OS-assigned free ports, and
# a collision with the avoided set is rejected at probe time.
set -euo pipefail

BENCH_REPO=$(cd "$(dirname "$0")/.." && pwd)
cd "$BENCH_REPO"

SUITE=suites/smoke.json
TASK_FILTER=
NO_RUNTIME=0
DOCTOR_ONLY=0
KEEP_RIG=0
EXTRA_RUN_ARGS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --suite)       SUITE=$2; shift 2 ;;
    --task)        TASK_FILTER=$2; shift 2 ;;
    --no-runtime)  NO_RUNTIME=1; shift ;;
    --doctor-only) DOCTOR_ONLY=1; shift ;;
    --keep-rig)    KEEP_RIG=1; shift ;;
    --down)        # teardown a rig kept with --keep-rig
                   RIGDIR=$2
                   [ -f "$RIGDIR/daemon.pid" ] && kill "$(cat "$RIGDIR/daemon.pid")" 2>/dev/null || true
                   sleep 2
                   [ -f "$RIGDIR/daemon.pid" ] && kill -9 "$(cat "$RIGDIR/daemon.pid")" 2>/dev/null || true
                   rm -rf "$RIGDIR"
                   echo "scratch-rig: removed $RIGDIR"
                   exit 0 ;;
    --)            shift; EXTRA_RUN_ARGS=("$@"); break ;;
    *)             echo "scratch-rig: unknown flag: $1" >&2; exit 2 ;;
  esac
done

MEEPT_REPO=${MEEPT_REPO:-$HOME/git/meept}
DAEMON_BIN=${MEEPT_DAEMON_BIN:-$MEEPT_REPO/bin/meept-daemon}
RIG_MODEL=${MEEPT_RIG_MODEL:-/Volumes/LLMs/LiquidAI/LFM2.5-8B-A1B-GGUF/LFM2.5-8B-A1B-Q4_K_M.gguf}
AGENTS_SRC=${MEEPT_RIG_AGENTS:-$MEEPT_REPO/config/agents}
REPO=${MEEPT_RIG_REPO:-$MEEPT_REPO}

[ -x "$DAEMON_BIN" ] || { echo "scratch-rig: daemon binary not found: $DAEMON_BIN (build it: cd $MEEPT_REPO && go build -o bin/meept-daemon ./cmd/meept-daemon)" >&2; exit 2; }

BENCH_BIN=${MEEPT_BENCH_BIN:-}
TMP_BENCH_DIR=
if [ -z "$BENCH_BIN" ]; then
  TMP_BENCH_DIR=$(mktemp -d)
  BENCH_BIN=$TMP_BENCH_DIR/meept-bench
  go build -o "$BENCH_BIN" ./cmd/meept-bench
fi
[ -x "$BENCH_BIN" ] || { echo "scratch-rig: bench binary not found: $BENCH_BIN" >&2; exit 2; }

# --- rig layout ------------------------------------------------------------
RIG=$(mktemp -d /tmp/meept-scratch-rig.XXXXXX)
RIG_HOME=$RIG/home/.meept          # MEEPT_HOME for the scratch daemon
RIG_SOCK=$RIG_HOME/meept.sock
SCRATCH=$RIG/worktrees             # bench worktree scratch root
mkdir -p "$RIG_HOME" "$RIG_HOME/run" "$SCRATCH"

# --- free-port probe (never the live runtime ports) ------------------------
AVOID_PORTS="8080 8081 8082 8083 8084 8143"
free_port() {
  python3 - "$@" <<'PYEOF'
import socket, sys
avoid = {int(p) for p in sys.argv[1:]}
for _ in range(50):
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    if port not in avoid:
        print(port)
        break
else:
    sys.exit("no free port outside the avoided set")
PYEOF
}
HTTP_PORT=$(free_port $AVOID_PORTS)
LLM_PORT=$(free_port $AVOID_PORTS)

# --- render configs (plain JSON is valid JSON5) ----------------------------
if [ "$NO_RUNTIME" = 1 ]; then
  LIFECYCLE='null'
else
  [ -f "$RIG_MODEL" ] || { echo "scratch-rig: model file missing: $RIG_MODEL (use --no-runtime for a model-less boot)" >&2; exit 2; }
  LIFECYCLE=$(MODEL_PATH="$RIG_MODEL" LLM_PORT="$LLM_PORT" PID_FILE="$RIG_HOME/run/llama.pid" python3 - <<'PYEOF'
import json, os
print(json.dumps({
    "runtime": "llama-cpp",
    "model_path": os.environ["MODEL_PATH"],
    "auto_start": True,
    "auto_stop_on_exit": True,
    "pid_file": os.environ["PID_FILE"],
    "spawn_command": ["llama-server", "--model", os.environ["MODEL_PATH"],
                      "--port", os.environ["LLM_PORT"], "--host", "127.0.0.1",
                      "--jinja", "--reasoning-budget", "0", "--ctx-size", "32768"],
    "spawn_timeout_seconds": 300,
    "health_check": {"endpoint": "/health", "interval_seconds": 10,
                     "timeout_seconds": 5, "unhealthy_threshold": 30},
}))
PYEOF
  )
fi

MODEL_NAME=$(basename "$RIG_MODEL")
python3 - "$RIG" "$RIG_HOME" "$RIG_SOCK" "$HTTP_PORT" "$LLM_PORT" "$MODEL_NAME" "$LIFECYCLE" "$SCRATCH" <<'PYEOF'
import json, sys
rig, home, sock, http_port, llm_port, model_name, lifecycle, scratch = sys.argv[1:9]
lifecycle = None if lifecycle == "null" else json.loads(lifecycle)

cfg = {
    "daemon": {"socket_path": sock, "pid_file": home + "/meept.pid",
               "data_dir": home, "log_level": "INFO"},
    "transport": {
        "rpc": {"enabled": True, "socket_path": sock},
        "http": {"enabled": True, "addr": "127.0.0.1:%s" % http_port,
                 "require_auth": False, "rest": True,
                 "websocket": False, "mcp": False},
    },
    "memory": {"data_dir": home + "/memory"},
    "projects": {"enabled": True, "base_dir": home + "/projects",
                 "auto_detect": False, "fence_enabled": False},
    "security": {"audit_db_path": home + "/audit.db",
                 "allowed_paths": [rig + "/**", scratch + "/**"]},
    "multiagent": {"enabled": True},
    "orchestrator": {"classifier_boot_fail_fast": True},
}
open(home + "/meept.json5", "w").write(json.dumps(cfg, indent=2))

provider = {
    "api": "openai",
    "options": {"baseURL": "http://127.0.0.1:%s/v1" % llm_port},
    "models": {model_name: {
        "name": model_name,
        "capabilities": ["completion", "code", "reasoning", "tool_use"],
        "input_cost": 0.0, "output_cost": 0.0,
        "context_limit": 32768, "max_output": 4096, "temperature": 0.7,
    }},
}
if lifecycle is not None:
    provider["lifecycle"] = lifecycle
ref = "rig/" + model_name
models = {
    "model": ref,
    "classifier_model": ref,
    "small_model": ref,
    "extract_model": "",
    "refusal_model": "",
    "default_timeout": 300,
    "disabled_providers": [],
    "model_aliases": {a: {"models": [ref], "timeout": 60, "max_fails": 3}
                      for a in ("classifier", "summarizer", "small", "coder",
                                "planner", "analyst")},
    "providers": {"rig": provider},
}
open(home + "/models.json5", "w").write(json.dumps(models, indent=2))
PYEOF

# --- agents registry (empty without it: every dispatch dies) ---------------
if [ -d "$AGENTS_SRC" ]; then
  cp -R "$AGENTS_SRC" "$RIG_HOME/agents"
else
  echo "scratch-rig: WARN agents dir not found: $AGENTS_SRC — dispatch may fail with 'no agent available'" >&2
fi

# --- provenance (written BEFORE launch) ------------------------------------
shasum -a 256 "$DAEMON_BIN" "$BENCH_BIN" "$RIG_HOME/meept.json5" \
              "$RIG_HOME/models.json5" "$SUITE" > "$RIG/provenance.sha256"
{
  echo "rig_dir=$RIG"
  echo "rig_home=$RIG_HOME"
  echo "socket=$RIG_SOCK"
  echo "http_port=$HTTP_PORT"
  echo "llm_port=$LLM_PORT"
  echo "no_runtime=$NO_RUNTIME"
  echo "daemon_bin=$DAEMON_BIN"
  echo "meept_repo=$MEEPT_REPO"
  echo "meept_head=$(git -C "$MEEPT_REPO" rev-parse HEAD 2>/dev/null || echo unknown)"
  echo "bench_head=$(git -C "$BENCH_REPO" rev-parse HEAD 2>/dev/null || echo unknown)"
} > "$RIG/provenance.json"

# --- teardown (trap EXIT) ---------------------------------------------------
DAEMON_PID=
cleanup() {
  rc=$?
  if [ "$KEEP_RIG" = 1 ]; then
    echo "scratch-rig: rig kept at $RIG (daemon pid ${DAEMON_PID:-?})"
    echo "  socket: $RIG_SOCK"
    echo "  teardown: scripts/scratch-rig.sh --down $RIG"
    return $rc
  fi
  if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
    # Graceful SIGTERM first: the daemon's shutdown takes its owned
    # llama-server children down with it (auto_stop_on_exit).
    kill "$DAEMON_PID" 2>/dev/null || true
    for _ in $(seq 1 30); do
      kill -0 "$DAEMON_PID" 2>/dev/null || break
      sleep 1
    done
    kill -9 "$DAEMON_PID" 2>/dev/null || true
  fi
  rm -rf "$RIG"
  [ -n "${TMP_BENCH_DIR:-}" ] && rm -rf "$TMP_BENCH_DIR"
  return $rc
}
trap cleanup EXIT

# --- launch the scratch daemon ---------------------------------------------
echo "scratch-rig: launching daemon (MEEPT_HOME=$RIG_HOME, socket=$RIG_SOCK)"
# `exec` inside the backgrounded subshell guarantees $! IS the daemon pid —
# without it $! can be a wrapper subshell that exits early, and cleanup
# rm -rf's the rig while the real daemon is still shutting down (it then
# recreates state dirs after the delete: observed leftover, 2026-10-22).
( cd "$RIG" && MEEPT_HOME="$RIG_HOME" HOME="$RIG/home" \
    exec "$DAEMON_BIN" -f > "$RIG/daemon.log" 2>&1 ) &
DAEMON_PID=$!
echo "$DAEMON_PID" > "$RIG/daemon.pid"

HEALTH_TIMEOUT=${MEEPT_RIG_HEALTH_TIMEOUT:-240}
if [ "$NO_RUNTIME" = 1 ]; then HEALTH_TIMEOUT=60; fi
MEEPT_BENCH_BIN="$BENCH_BIN" ./scripts/wait-for-daemon.sh "$RIG_SOCK" "$HEALTH_TIMEOUT"

export MEEPT_BENCH_SOCKET=$RIG_SOCK
export JUDGE_URL="http://127.0.0.1:$LLM_PORT"

if [ "$DOCTOR_ONLY" = 1 ]; then
  "$BENCH_BIN" doctor
  exit 0
fi

# --- run the bench ----------------------------------------------------------
RUN_ARGS=(run --suite "$SUITE" --repo "$REPO" --scratch "$SCRATCH"
          --out "$RIG/results" --auto-approved
          --judge-cmd "python3 scripts/judge-local.py")
[ -n "$TASK_FILTER" ] && RUN_ARGS+=(--task "$TASK_FILTER")
RUN_ARGS+=("${EXTRA_RUN_ARGS[@]+"${EXTRA_RUN_ARGS[@]}"}")

echo "scratch-rig: $BENCH_BIN ${RUN_ARGS[*]}"
rc=0
"$BENCH_BIN" "${RUN_ARGS[@]}" || rc=$?
echo "scratch-rig: bench exit=$rc; results in $RIG/results (copied to stdout dir below if kept)"
if [ "$KEEP_RIG" != 1 ] && [ -d "$RIG/results" ]; then
  DEST="results/scratch-rig-$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$DEST"
  cp -R "$RIG/results/." "$DEST/" && echo "scratch-rig: results saved to $DEST"
fi
exit $rc
