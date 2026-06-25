#!/usr/bin/env bash
# Start all Popin services in the background with logging.
# Usage: ./scripts/start.sh
#   --docker   use docker compose instead of local processes
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$PWD"

mkdir -p scripts/logs

if [[ "${1:-}" == "--docker" ]]; then
  echo "Starting via docker compose..."
  docker compose up --build -d
  echo "Docker services started. Web: http://localhost:3000"
  exit 0
fi

require() {
  command -v "$1" >/dev/null 2>&1 || { echo "Error: '$1' not found in PATH" >&2; exit 1; }
}
require go
require npm
if ! command -v livekit-server >/dev/null 2>&1; then
  echo "Warning: livekit-server not found. Install: brew install livekit/tap/livekit-server" >&2
fi

# Ensure .env exists
if [[ ! -f .env ]]; then
  if [[ -f .env.example ]]; then
    cp .env.example .env
    echo "Created .env from .env.example"
  else
    echo "Error: no .env or .env.example found" >&2; exit 1
  fi
fi

# Ensure web deps installed
if [[ ! -d web/node_modules ]]; then
  echo "Installing web dependencies..."
  (cd web && npm install)
fi

start() {
  local name="$1"; shift
  echo "Starting $name..."
  nohup "$@" >"$ROOT/scripts/logs/$name.log" 2>&1 &
  echo $! >"$ROOT/scripts/logs/$name.pid"
  echo "  pid=$! log=scripts/logs/$name.log"
}

start livekit livekit-server --config livekit.yaml --dev || true
start backend go run ./cmd/server
(cd web && start web npm run dev)

echo
echo "All services started:"
echo "  Web:      http://localhost:3000"
echo "  Backend:  http://localhost:8080"
echo "  LiveKit:  ws://localhost:7880"
echo
echo "Logs: scripts/logs/*.log"
echo "Stop: ./scripts/stop.sh"
echo "Status: ./scripts/status.sh"