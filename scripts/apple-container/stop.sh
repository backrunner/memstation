#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"

PURGE=0
if [[ "${1:-}" == "--purge" ]]; then
  PURGE=1
fi

memstation_apple_require_container

for name in "$MEMSTATION_APPLE_WORKSPACE" "$MEMSTATION_APPLE_REDIS" "$MEMSTATION_APPLE_POSTGRES"; do
  memstation_apple_remove_container "$name"
done

if [[ "$PURGE" == "1" ]]; then
  container volume delete "$MEMSTATION_APPLE_REDIS_VOLUME" >/dev/null 2>&1 || true
  container volume delete "$MEMSTATION_APPLE_POSTGRES_VOLUME" >/dev/null 2>&1 || true
  container network delete "$MEMSTATION_APPLE_NETWORK" >/dev/null 2>&1 || true
  echo "Apple container workspace and persistent development data removed."
else
  echo "Apple container workspace stopped. PostgreSQL and Redis volumes were preserved."
fi
