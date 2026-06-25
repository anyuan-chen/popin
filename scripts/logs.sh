#!/usr/bin/env bash
# Tail all service logs.
# Usage: ./scripts/logs.sh [service]
#   default tails all logs; pass a name (livekit|backend|web) to tail one.
set -uo pipefail
cd "$(dirname "$0")/.."

if [[ $# -ge 1 ]]; then
  exec tail -f "scripts/logs/$1.log"
fi

found=0
for f in scripts/logs/livekit.log scripts/logs/backend.log scripts/logs/web.log; do
  [[ -f "$f" ]] || continue
  found=1
  ( tail -F "$f" | sed "s/^/$(basename "$f" .log): /" ) &
done
if [[ $found -eq 0 ]]; then
  echo "No log files in scripts/logs/ yet. Run ./scripts/start.sh first." >&2
  exit 1
fi
trap 'kill $(jobs -p) 2>/dev/null' EXIT
wait