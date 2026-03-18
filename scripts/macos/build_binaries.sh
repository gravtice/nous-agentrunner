#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
mkdir -p "${DIST_DIR}"

if ! command -v go >/dev/null 2>&1; then
  echo "go not found in PATH" >&2
  exit 1
fi

read_go_version_from_modfile() {
  local modfile="$1"
  awk '
    $1 == "go" {
      print $2
      exit
    }
  ' "$modfile" 2>/dev/null
}

normalize_go_semver() {
  local v="$1"
  if [[ "$v" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    echo "${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.${BASH_REMATCH[3]}"
    return 0
  fi
  if [[ "$v" =~ ^([0-9]+)\.([0-9]+)$ ]]; then
    echo "${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.0"
    return 0
  fi
  echo ""
  return 0
}

semver_ge() {
  local a="$1"
  local b="$2"
  local a1 a2 a3 b1 b2 b3
  IFS=. read -r a1 a2 a3 <<<"$a"
  IFS=. read -r b1 b2 b3 <<<"$b"
  a3="${a3:-0}"
  b3="${b3:-0}"

  if [ "${a1}" -ne "${b1}" ]; then
    [ "${a1}" -gt "${b1}" ]
    return $?
  fi
  if [ "${a2}" -ne "${b2}" ]; then
    [ "${a2}" -gt "${b2}" ]
    return $?
  fi
  [ "${a3}" -ge "${b3}" ]
}

setup_go_toolchain() {
  # Some `go` versions will try to auto-download "go1.xx" (without patch),
  # but published toolchains are `go1.xx.0+`. Also, we build nested modules
  # (e.g. references/lima) that may require a newer Go than the host provides.
  local required=""
  local required_root=""
  local required_lima=""

  if [ -f "${ROOT_DIR}/go.mod" ]; then
    required_root="$(read_go_version_from_modfile "${ROOT_DIR}/go.mod")"
    required_root="$(normalize_go_semver "$required_root")"
  fi
  if [ -f "${ROOT_DIR}/references/lima/go.mod" ]; then
    required_lima="$(read_go_version_from_modfile "${ROOT_DIR}/references/lima/go.mod")"
    required_lima="$(normalize_go_semver "$required_lima")"
  fi

  required="$required_root"
  if [ -n "$required_lima" ]; then
    if [ -z "$required" ] || semver_ge "$required_lima" "$required"; then
      required="$required_lima"
    fi
  fi

  if [ -z "$required" ]; then
    return 0
  fi

  local current=""
  current="$(cd / && go env GOVERSION 2>/dev/null || true)"
  current="${current#go}"
  current="$(normalize_go_semver "$current")"
  if [ -z "$current" ]; then
    export GOTOOLCHAIN="go${required}"
    return 0
  fi

  if semver_ge "$current" "$required"; then
    return 0
  fi

  export GOTOOLCHAIN="go${required}"
}

setup_go_toolchain

echo "[1/4] build agent-runnerd (darwin/arm64)"
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
  go build -o "${DIST_DIR}/agent-runnerd" "${ROOT_DIR}/cmd/agent-runnerd"

echo "[2/4] build guest-runnerd (linux/arm64)"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -o "${DIST_DIR}/guest-runnerd" "${ROOT_DIR}/cmd/guest-runnerd"

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
