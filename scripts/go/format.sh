#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
APP_DIR="$ROOT_DIR/apps/server"
GOFUMPT_VERSION=${GOFUMPT_VERSION:-v0.9.2}
GOIMPORTS_VERSION=${GOIMPORTS_VERSION:-v0.43.0}
MODE=${1:-write}

collect_go_files() {
  while IFS= read -r -d '' file; do
    GO_FILES+=("$file")
  done < <(
    find "$APP_DIR" \
      \( -path "$APP_DIR/ent" -o -path "$APP_DIR/ent/*" -o -path "$APP_DIR/graph/generated" -o -path "$APP_DIR/graph/generated/*" -o -path "$APP_DIR/dist" -o -path "$APP_DIR/dist/*" \) -prune \
      -o -type f -name '*.go' -print0
  )
}

declare -a GO_FILES=()
collect_go_files

if [[ ${#GO_FILES[@]} -eq 0 ]]; then
  exit 0
fi

case "$MODE" in
  write)
    go run "golang.org/x/tools/cmd/goimports@${GOIMPORTS_VERSION}" -w "${GO_FILES[@]}"
    go run "mvdan.cc/gofumpt@${GOFUMPT_VERSION}" -w "${GO_FILES[@]}"
    ;;
  --check|check)
    goimports_out=$(go run "golang.org/x/tools/cmd/goimports@${GOIMPORTS_VERSION}" -l "${GO_FILES[@]}")
    gofumpt_out=$(go run "mvdan.cc/gofumpt@${GOFUMPT_VERSION}" -l "${GO_FILES[@]}")

    if [[ -n "$goimports_out" ]]; then
      echo "goimports reported unformatted files:"
      echo "$goimports_out"
    fi
    if [[ -n "$gofumpt_out" ]]; then
      echo "gofumpt reported unformatted files:"
      echo "$gofumpt_out"
    fi
    if [[ -n "$goimports_out" || -n "$gofumpt_out" ]]; then
      exit 1
    fi
    ;;
  *)
    echo "usage: $(basename "$0") [write|--check]" >&2
    exit 1
    ;;
esac
