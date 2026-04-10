#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"

if memstation_inside_devcontainer; then
  cd "$MEMSTATION_DEVCONTAINER_WORKSPACE"
  exec bash ./scripts/dev/run-fullstack-workspace.sh "$@"
fi

memstation_require_host_command docker

remote_env_args=()
memstation_collect_remote_env_args remote_env_args

"$SCRIPT_DIR/start-workspace.sh"

cd "$ROOT_DIR"
memstation_run_devcontainer exec \
  --workspace-folder "$ROOT_DIR" \
  "${remote_env_args[@]}" \
  bash -lc "cd ${MEMSTATION_DEVCONTAINER_WORKSPACE} && exec bash ./scripts/dev/run-fullstack-workspace.sh"
