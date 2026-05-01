#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"

PORTLESS_NAME=${MEMSTATION_MANAGER_PORTLESS_NAME:-memstation-manager}
MANAGER_PORT=${MEMSTATION_MANAGER_DEV_PORT:-5173}
SERVER_PORT=${MEMSTATION_PORT:-8080}
SKIP_ALIAS=${MEMSTATION_SKIP_PORTLESS_ALIAS:-0}

if memstation_inside_devcontainer; then
  echo "This command must be run on the host so Portless can register the localhost alias." >&2
  echo "Inside the devcontainer, use: pnpm dev:workspace" >&2
  exit 1
fi

memstation_require_host_command docker
memstation_require_host_command pnpm

remote_env_args=()

export MEMSTATION_MANAGER_DEV_PORT="$MANAGER_PORT"
export MEMSTATION_MANAGER_DEV_PROXY_TARGET=${MEMSTATION_MANAGER_DEV_PROXY_TARGET:-http://127.0.0.1:${SERVER_PORT}}
export PUBLIC_MEMSTATION_DEFAULT_TARGET=${PUBLIC_MEMSTATION_DEFAULT_TARGET:-https://${PORTLESS_NAME}.localhost}

memstation_collect_remote_env_args remote_env_args

"$SCRIPT_DIR/start-workspace.sh"

if [[ "$SKIP_ALIAS" != "1" ]]; then
  echo "Ensuring Portless proxy is running..."
  pnpm exec portless proxy start
  echo "Registering Portless alias for manager dev server..."
  pnpm exec portless alias "$PORTLESS_NAME" "$MANAGER_PORT" --force
fi

echo "Starting MemStation full-stack debug inside the devcontainer..."
echo "- Manager: https://${PORTLESS_NAME}.localhost"
echo "- Manager forwarded port: http://127.0.0.1:${MANAGER_PORT}"
echo "- Server forwarded port: http://127.0.0.1:${SERVER_PORT}"
echo "- Container API proxy target: ${MEMSTATION_MANAGER_DEV_PROXY_TARGET}"
echo
echo "Use Ctrl-C to stop the in-container dev server. The Portless alias remains registered."

cd "$ROOT_DIR"
memstation_run_devcontainer exec \
  --workspace-folder "$ROOT_DIR" \
  "${remote_env_args[@]}" \
  bash -lc "cd ${MEMSTATION_DEVCONTAINER_WORKSPACE} && exec bash ./scripts/dev/run-fullstack-workspace.sh"
