#!/usr/bin/env bash
# wait-for-daemon.sh <socket> [timeout-seconds]
#
# Block until `meept-bench doctor` reaches the meept daemon on <socket>.
# doctor reads the socket path from MEEPT_BENCH_SOCKET; this script exports
# it, so callers only pass the path.
#
# Env overrides:
#   MEEPT_BENCH_BIN      bench binary to invoke        (default: ./meept-bench)
#   MEEPT_POLL_INTERVAL  seconds between attempts      (default: 2)
#
# Exit codes: 0 daemon ready, 1 timed out, 2 usage/binary error.
set -euo pipefail

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
  echo "usage: wait-for-daemon.sh <socket> [timeout-seconds]" >&2
  exit 2
fi

SOCKET=$1
TIMEOUT=${2:-60}
BENCH=${MEEPT_BENCH_BIN:-./meept-bench}
INTERVAL=${MEEPT_POLL_INTERVAL:-2}

if ! command -v "$BENCH" >/dev/null 2>&1 && [ ! -x "$BENCH" ]; then
  echo "wait-for-daemon: bench binary not found: $BENCH" >&2
  exit 2
fi

export MEEPT_BENCH_SOCKET=$SOCKET
deadline=$(( $(date +%s) + TIMEOUT ))
last=$(mktemp)
trap 'rm -f "$last"' EXIT

attempts=0
until "$BENCH" doctor >"$last" 2>&1; do
  attempts=$((attempts + 1))
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "wait-for-daemon: daemon not ready on $SOCKET after ${TIMEOUT}s ($attempts attempts)" >&2
    echo "--- last doctor output ---" >&2
    cat "$last" >&2
    exit 1
  fi
  sleep "$INTERVAL"
done

echo "wait-for-daemon: daemon ready on $SOCKET (attempts: $attempts)"
cat "$last"
