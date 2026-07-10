#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)

MEMSTATION_APPLE_NETWORK=${MEMSTATION_APPLE_NETWORK:-memstation-dev}
MEMSTATION_APPLE_WORKSPACE_IMAGE=${MEMSTATION_APPLE_WORKSPACE_IMAGE:-memstation-dev-workspace:local}
MEMSTATION_APPLE_WORKSPACE=${MEMSTATION_APPLE_WORKSPACE:-memstation-dev-workspace}
MEMSTATION_APPLE_POSTGRES=${MEMSTATION_APPLE_POSTGRES:-memstation-dev-postgres}
MEMSTATION_APPLE_REDIS=${MEMSTATION_APPLE_REDIS:-memstation-dev-redis}
MEMSTATION_APPLE_POSTGRES_VOLUME=${MEMSTATION_APPLE_POSTGRES_VOLUME:-memstation-dev-postgres-data}
MEMSTATION_APPLE_REDIS_VOLUME=${MEMSTATION_APPLE_REDIS_VOLUME:-memstation-dev-redis-data}
MEMSTATION_APPLE_WORKSPACE_DIR=/workspace/memstation

memstation_apple_require_container() {
  if ! command -v container >/dev/null 2>&1; then
    echo "Apple container CLI is required. Install container 1.0.0 or newer first." >&2
    exit 1
  fi
}

memstation_apple_ensure_system() {
  if ! container system status >/dev/null 2>&1; then
    echo "Starting Apple container services..."
    container system start --disable-kernel-install
  fi
}

memstation_apple_container_exists() {
  container inspect "$1" >/dev/null 2>&1
}

memstation_apple_network_exists() {
  container network inspect "$1" >/dev/null 2>&1
}

memstation_apple_volume_exists() {
  container volume inspect "$1" >/dev/null 2>&1
}

memstation_apple_remove_container() {
  local name=$1
  if memstation_apple_container_exists "$name"; then
    container delete --force "$name" >/dev/null
  fi
}

memstation_apple_wait_for_exec() {
  local name=$1
  local description=$2
  shift 2

  local attempt
  for attempt in $(seq 1 60); do
    if container exec "$name" "$@" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "Timed out waiting for ${description}." >&2
  container logs "$name" >&2 || true
  return 1
}
