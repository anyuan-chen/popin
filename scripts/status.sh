#!/usr/bin/env bash
# Show status of Popin services.
# Usage: ./scripts/status.sh
set -uo pipefail
cd "$(dirname "$0")/.."

check_port() {
  local name="$1" port="$2"
  if nc -z localhost "$port" 2>/dev/null; then
    printf "  \033[32m●\033[0m %-10s :%s  up\n" "$name" "$port"
  else
    printf "  \033[31m○\033[0m %-10s :%s  down\n" "$name" "$port"
  fi
}

echo "Popin services:"
check_port livekit 7880
check_port backend 8080
check_port web     3000

echo
echo "Processes:"
for name in livekit backend web; do
  pidfile="scripts/logs/$name.pid"
  if [[ -f "$pidfile" ]]; then
    pid="$(cat "$pidfile")"
    if kill -0 "$pid" 2>/dev/null; then
      ps -o pid,etime,command -p "$pid" 2>/dev/null | tail -n +1
    fi
  fi
done