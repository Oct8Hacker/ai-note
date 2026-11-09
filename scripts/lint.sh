#!/usr/bin/env bash
# Reproduces the CI lint job locally (golangci-lint v2.1.6 via Docker).
set -euo pipefail
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1
run() {
  docker run --rm -v "$PWD:/app" -w /app golangci/golangci-lint:v2.1.6 golangci-lint run "$@" ./...
}
run
run --build-tags=integration
