#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"

POSTGRES_IMAGE=${MEMSTATION_APPLE_POSTGRES_IMAGE:-pgvector/pgvector:pg16}
POSTGRES_DATA_DIR=${MEMSTATION_APPLE_POSTGRES_DATA_DIR:-/var/lib/postgresql/data/pgdata}
REDIS_IMAGE=${MEMSTATION_APPLE_REDIS_IMAGE:-redis:7}
SKIP_BUILD=${MEMSTATION_APPLE_SKIP_BUILD:-0}
HOST_HTTP_PORT=${MEMSTATION_APPLE_HTTP_PORT:-8080}
HOST_GRPC_PORT=${MEMSTATION_APPLE_GRPC_PORT:-9090}
HOST_MANAGER_PORT=${MEMSTATION_APPLE_MANAGER_PORT:-5173}
HOST_POSTGRES_PORT=${MEMSTATION_APPLE_POSTGRES_PORT:-5432}
HOST_REDIS_PORT=${MEMSTATION_APPLE_REDIS_PORT:-6379}

memstation_apple_require_container
memstation_apple_ensure_system

if ! memstation_apple_network_exists "$MEMSTATION_APPLE_NETWORK"; then
  echo "Creating Apple container network ${MEMSTATION_APPLE_NETWORK}..."
  container network create "$MEMSTATION_APPLE_NETWORK" >/dev/null
fi

if ! memstation_apple_volume_exists "$MEMSTATION_APPLE_POSTGRES_VOLUME"; then
  container volume create "$MEMSTATION_APPLE_POSTGRES_VOLUME" >/dev/null
fi
if ! memstation_apple_volume_exists "$MEMSTATION_APPLE_REDIS_VOLUME"; then
  container volume create "$MEMSTATION_APPLE_REDIS_VOLUME" >/dev/null
fi

if ! memstation_apple_container_exists "$MEMSTATION_APPLE_POSTGRES"; then
  echo "Starting PostgreSQL in Apple container..."
  container run --detach \
    --name "$MEMSTATION_APPLE_POSTGRES" \
    --network "$MEMSTATION_APPLE_NETWORK" \
    --publish "127.0.0.1:${HOST_POSTGRES_PORT}:5432" \
    --env POSTGRES_USER=memstation \
    --env POSTGRES_PASSWORD=memstation_dev \
    --env POSTGRES_DB=memstation \
    --env PGDATA="$POSTGRES_DATA_DIR" \
    --volume "${MEMSTATION_APPLE_POSTGRES_VOLUME}:/var/lib/postgresql/data" \
    "$POSTGRES_IMAGE" >/dev/null
else
  container start "$MEMSTATION_APPLE_POSTGRES" >/dev/null 2>&1 || true
fi

if ! memstation_apple_container_exists "$MEMSTATION_APPLE_REDIS"; then
  echo "Starting Redis in Apple container..."
  container run --detach \
    --name "$MEMSTATION_APPLE_REDIS" \
    --network "$MEMSTATION_APPLE_NETWORK" \
    --publish "127.0.0.1:${HOST_REDIS_PORT}:6379" \
    --volume "${MEMSTATION_APPLE_REDIS_VOLUME}:/data" \
    "$REDIS_IMAGE" redis-server --save 60 1 --appendonly yes >/dev/null
else
  container start "$MEMSTATION_APPLE_REDIS" >/dev/null 2>&1 || true
fi

memstation_apple_wait_for_exec "$MEMSTATION_APPLE_POSTGRES" PostgreSQL \
  pg_isready -U memstation -d memstation
memstation_apple_wait_for_exec "$MEMSTATION_APPLE_REDIS" Redis redis-cli ping

if [[ "$SKIP_BUILD" != "1" ]]; then
  echo "Building MemStation workspace image with Apple container..."
  container build \
    --tag "$MEMSTATION_APPLE_WORKSPACE_IMAGE" \
    --file "$ROOT_DIR/.devcontainer/Dockerfile" \
    "$ROOT_DIR"
fi

# Recreate only the stateless workspace so image, ports, and environment stay in sync.
memstation_apple_remove_container "$MEMSTATION_APPLE_WORKSPACE"

echo "Starting MemStation Apple workspace container..."
container run --detach \
  --name "$MEMSTATION_APPLE_WORKSPACE" \
  --network "$MEMSTATION_APPLE_NETWORK" \
  --workdir "$MEMSTATION_APPLE_WORKSPACE_DIR" \
  --publish "127.0.0.1:${HOST_HTTP_PORT}:8080" \
  --publish "127.0.0.1:${HOST_GRPC_PORT}:9090" \
  --publish "127.0.0.1:${HOST_MANAGER_PORT}:5173" \
  --env MEMSTATION_APPLE_CONTAINER=1 \
  --env MEMSTATION_PORT=8080 \
  --env MEMSTATION_GRPC_PORT=9090 \
  --env DATABASE_URL="postgres://memstation:memstation_dev@${MEMSTATION_APPLE_POSTGRES}:5432/memstation?sslmode=disable" \
  --env REDIS_URL="redis://${MEMSTATION_APPLE_REDIS}:6379/0" \
  --env VECTOR_STORE_BACKEND=pgvector \
  --env MEMSTATION_ADMIN_TOKEN="${MEMSTATION_ADMIN_TOKEN:-memstation-local-admin-token}" \
  --env MEMSTATION_MASTER_KEY="${MEMSTATION_MASTER_KEY:-memstation-local-master-key}" \
  --env MEMSTATION_CORS_ALLOWED_ORIGINS="${MEMSTATION_CORS_ALLOWED_ORIGINS:-}" \
  --env MEMSTATION_SESSION_COOKIE_SECURE=false \
  --env MEMSTATION_SESSION_COOKIE_CROSS_SITE=false \
  --mount "type=bind,source=${ROOT_DIR},target=${MEMSTATION_APPLE_WORKSPACE_DIR}" \
  "$MEMSTATION_APPLE_WORKSPACE_IMAGE" sleep infinity >/dev/null

echo "Apple workspace is ready."
echo "- Workspace: ${MEMSTATION_APPLE_WORKSPACE_DIR}"
echo "- Server debug URL: http://127.0.0.1:${HOST_HTTP_PORT}"
echo "- Manager debug URL: http://127.0.0.1:${HOST_MANAGER_PORT}"
