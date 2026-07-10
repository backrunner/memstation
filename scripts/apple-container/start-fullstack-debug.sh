#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"

"$SCRIPT_DIR/start-workspace.sh"

echo "Starting MemStation full-stack debug in Apple container..."
echo "Use Ctrl-C to stop the debug processes; infrastructure containers remain available."

exec container exec --interactive \
  --workdir "$MEMSTATION_APPLE_WORKSPACE_DIR" \
  "$MEMSTATION_APPLE_WORKSPACE" \
  bash ./scripts/dev/run-fullstack-workspace.sh "$@"
