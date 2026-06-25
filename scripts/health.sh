#!/usr/bin/env bash
# Quick health check + setup sanity for Popin services.
# Usage: ./scripts/health.sh
set -uo pipefail
cd "$(dirname "$0")/.."

pass=0; fail=0
check() {
  local label="$1"; shift
  if "$@" >/dev/null 2>&1; then
    printf "  \033[32m●\033[0m %s\n" "$label"
    pass=$((pass+1))
  else
    printf "  \033[31m○\033[0m %s\n" "$label"
    fail=$((fail+1))
  fi
}

echo "Tools:"
check "go installed"     command -v go
check "npm installed"    command -v npm
check "livekit-server"   command -v livekit-server
check "docker"           command -v docker

echo
echo "Config:"
check ".env present"          test -f .env
check "livekit.yaml present"  test -f livekit.yaml
check "web/node_modules"      test -d web/node_modules

echo
echo "Services (HTTP):"
if nc -z localhost 8080 2>/dev/null; then
  curl -fsS http://localhost:8080/health && echo "" && pass=$((pass+1)) || fail=$((fail+1))
else
  echo "  backend down - skipping /health"
  fail=$((fail+1))
fi

echo
echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]