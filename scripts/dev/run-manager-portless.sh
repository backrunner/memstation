#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
PORTLESS_NAME=${MEMSTATION_MANAGER_PORTLESS_NAME:-memstation-manager}
PROXY_TARGET=${MEMSTATION_MANAGER_DEV_PROXY_TARGET:-http://127.0.0.1:8080}

export MEMSTATION_MANAGER_DEV_PROXY_TARGET="$PROXY_TARGET"
export PUBLIC_MEMSTATION_DEFAULT_TARGET=${PUBLIC_MEMSTATION_DEFAULT_TARGET:-https://${PORTLESS_NAME}.localhost}

cd "$ROOT_DIR"

echo "Starting MemStation manager through Portless..."
echo "- Manager: https://${PORTLESS_NAME}.localhost"
echo "- API proxy target: ${MEMSTATION_MANAGER_DEV_PROXY_TARGET}"
echo "- Connection URI default: ${PUBLIC_MEMSTATION_DEFAULT_TARGET}"

exec pnpm exec portless run --name "$PORTLESS_NAME" -- pnpm --filter @memstation/manager exec vite dev --host 127.0.0.1
