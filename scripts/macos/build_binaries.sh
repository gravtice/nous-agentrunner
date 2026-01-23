#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
mkdir -p "${DIST_DIR}"

if ! command -v go >/dev/null 2>&1; then
  echo "go not found in PATH" >&2
  exit 1
fi

echo "[1/4] build nous-agent-runnerd (darwin/arm64)"
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
  go build -o "${DIST_DIR}/nous-agent-runnerd" "${ROOT_DIR}/cmd/nous-agent-runnerd"

echo "[2/4] build nous-guest-runnerd (linux/arm64)"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -o "${DIST_DIR}/nous-guest-runnerd" "${ROOT_DIR}/cmd/nous-guest-runnerd"

echo "[3/4] build limactl (darwin/arm64)"
pushd "${ROOT_DIR}/references/lima" >/dev/null
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 \
  go build -o "${DIST_DIR}/limactl" ./cmd/limactl
popd >/dev/null

echo "[4/5] build lima-guestagent (linux/arm64)"
pushd "${ROOT_DIR}/references/lima" >/dev/null
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -o "${DIST_DIR}/lima-guestagent.Linux-aarch64" ./cmd/lima-guestagent
popd >/dev/null

echo "[5/5] stage lima templates"
rm -rf "${DIST_DIR}/lima-templates"
ditto "${ROOT_DIR}/references/lima/templates" "${DIST_DIR}/lima-templates"

echo "OK: ${DIST_DIR}"
