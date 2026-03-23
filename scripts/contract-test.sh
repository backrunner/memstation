#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)

cd "$ROOT_DIR/apps/server"
go test ./tests/contract/... -count=1
