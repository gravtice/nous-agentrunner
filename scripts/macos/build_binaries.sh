#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
mkdir -p "${DIST_DIR}"

if ! command -v go >/dev/null 2>&1; then
  echo "go not found in PATH" >&2
  exit 1
fi

echo "[1/3] build nous-agent-runnerd (darwin/arm64)"
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
  go build -o "${DIST_DIR}/nous-agent-runnerd" "${ROOT_DIR}/cmd/nous-agent-runnerd"

echo "[2/3] build nous-guest-runnerd (linux/arm64)"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -o "${DIST_DIR}/nous-guest-runnerd" "${ROOT_DIR}/cmd/nous-guest-runnerd"

echo "[3/3] build limactl (darwin/arm64)"
pushd "${ROOT_DIR}/references/lima" >/dev/null
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 \
  go build -o "${DIST_DIR}/limactl" ./cmd/limactl
popd >/dev/null

echo "OK: ${DIST_DIR}"

