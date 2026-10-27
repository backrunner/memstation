#!/usr/bin/env bash

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
DEVCONTAINER_CLI_VERSION=${DEVCONTAINER_CLI_VERSION:-0.85.0}
MEMSTATION_DEVCONTAINER_WORKSPACE=${MEMSTATION_DEVCONTAINER_WORKSPACE:-/workspace/memstation}
MEMSTATION_DEVCONTAINER_REMOTE_ENV_VARS=(
  MEMSTATION_ADMIN_TOKEN
  MEMSTATION_MASTER_KEY
  CHAT_MODEL
  SUMMARY_MODEL
  ANALYSIS_MODEL
  EMBEDDING_MODEL
  OPENAI_API_KEY
  OPENAI_BASE_URL
  ANTHROPIC_API_KEY
  ANTHROPIC_BASE_URL
  GEMINI_API_KEY
  GEMINI_BASE_URL
  MEMSTATION_PROVIDER_TIMEOUT_SECONDS
  MEMSTATION_MANAGER_DEV_PORT
  MEMSTATION_MANAGER_DEV_PROXY_TARGET
  PUBLIC_MEMSTATION_DEFAULT_TARGET
  MEMSTATION_CORS_ALLOWED_ORIGINS
  MEMSTATION_SKIP_PNPM_INSTALL
  MEMSTATION_SKIP_DEV_MIGRATE
)

memstation_inside_devcontainer() {
  [[ -f /.dockerenv ]] && [[ -d "$MEMSTATION_DEVCONTAINER_WORKSPACE" ]]
}

memstation_require_host_command() {
  local name=$1
  if ! command -v "$name" >/dev/null 2>&1; then
    echo "required command not found: $name" >&2
    exit 1
  fi
}

memstation_collect_remote_env_args() {
  local -n target=$1
  local name
  for name in "${MEMSTATION_DEVCONTAINER_REMOTE_ENV_VARS[@]}"; do
    if [[ -n "${!name:-}" ]]; then
      target+=(--remote-env "$name=${!name}")
    fi
  done
}

memstation_run_devcontainer() {
  if command -v devcontainer >/dev/null 2>&1; then
    devcontainer "$@"
    return
  fi
  memstation_require_host_command pnpm
  pnpm dlx "@devcontainers/cli@${DEVCONTAINER_CLI_VERSION}" "$@"
}
