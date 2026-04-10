#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"

if memstation_inside_devcontainer; then
  echo "MemStation devcontainer is already running."
  exit 0
fi

memstation_require_host_command docker

remote_env_args=()
memstation_collect_remote_env_args remote_env_args

echo "Starting MemStation devcontainer workspace..."
memstation_run_devcontainer up --workspace-folder "$ROOT_DIR" "${remote_env_args[@]}"

echo "Devcontainer workspace is ready."
echo "- Workspace: $MEMSTATION_DEVCONTAINER_WORKSPACE"
echo "- Server debug URL: http://127.0.0.1:8080"
echo "- Manager debug URL: http://127.0.0.1:5173"
