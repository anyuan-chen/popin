#!/usr/bin/env bash
# Restart all services (stop.sh then start.sh).
# Usage: ./scripts/restart.sh [--docker]
set -euo pipefail
cd "$(dirname "$0")/.."
"$(dirname "$0")"/stop.sh "${@:-}"
sleep 1
"$(dirname "$0")"/start.sh "${@:-}"