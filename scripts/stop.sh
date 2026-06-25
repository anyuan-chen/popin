#!/usr/bin/env bash
# Stop all Popin services started by start.sh.
# Usage: ./scripts/stop.sh [--docker]
set -uo pipefail
cd "$(dirname "$0")/.."

if [[ "${1:-}" == "--docker" ]]; then
  docker compose down
  exit 0
fi

for name in livekit backend web; do
  pidfile="scripts/logs/$name.pid"
  if [[ -f "$pidfile" ]]; then
    pid="$(cat "$pidfile")"
    if kill -0 "$pid" 2>/dev/null; then
      echo "Stopping $name (pid=$pid)..."
      kill "$pid" 2>/dev/null || true
      # Kill the process group so children (go run, npm) die too
      pkill -P "$pid" 2>/dev/null || true
    else
      echo "$name not running (stale pidfile)"
    fi
    rm -f "$pidfile"
  fi
done
echo "Stopped."