#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
APP_DIR="$ROOT_DIR/apps/server"
GOLANGCI_LINT_VERSION=${GOLANGCI_LINT_VERSION:-v1.64.8}

cd "$APP_DIR"

if [[ $# -eq 0 ]]; then
  set -- ./...
fi

exec go run "github.com/golangci/golangci-lint/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}" run --config "$APP_DIR/.golangci.yml" "$@"
