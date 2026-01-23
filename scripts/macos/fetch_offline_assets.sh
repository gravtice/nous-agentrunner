#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
ASSETS_DIR="${DIST_DIR}/offline-assets"

fail() {
  echo "error: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 not found in PATH"
}

require_cmd curl
require_cmd awk

mkdir -p "${ASSETS_DIR}"

DEBIAN_YAML="${ROOT_DIR}/references/lima/templates/_images/debian-12.yaml"
CONTAINERD_YAML="${ROOT_DIR}/references/lima/pkg/limayaml/containerd.yaml"
[ -f "${DEBIAN_YAML}" ] || fail "missing: ${DEBIAN_YAML}"
[ -f "${CONTAINERD_YAML}" ] || fail "missing: ${CONTAINERD_YAML}"

read -r VM_IMAGE_URL VM_IMAGE_DIGEST < <(
  awk '
    $1=="-" && $2=="location:" {loc=$3; gsub(/"/,"",loc); arch=""; dig=""; next}
    $1=="arch:" {arch=$2; gsub(/"/,"",arch); next}
    $1=="digest:" {dig=$2; gsub(/"/,"",dig); next}
    loc!="" && arch=="aarch64" && dig!="" {print loc, dig; exit}
  ' "${DEBIAN_YAML}"
)
[ -n "${VM_IMAGE_URL:-}" ] || fail "failed to parse aarch64 image location from: ${DEBIAN_YAML}"
[ -n "${VM_IMAGE_DIGEST:-}" ] || fail "failed to parse aarch64 image digest from: ${DEBIAN_YAML}"

read -r NERDCTL_URL NERDCTL_DIGEST < <(
  awk '
    $1=="-" && $2=="location:" {loc=$3; gsub(/"/,"",loc); arch=""; dig=""; next}
    $1=="arch:" {arch=$2; gsub(/"/,"",arch); next}
    $1=="digest:" {dig=$2; gsub(/"/,"",dig); next}
    loc!="" && arch=="aarch64" && dig!="" {print loc, dig; exit}
  ' "${CONTAINERD_YAML}"
)
[ -n "${NERDCTL_URL:-}" ] || fail "failed to parse aarch64 nerdctl archive location from: ${CONTAINERD_YAML}"
[ -n "${NERDCTL_DIGEST:-}" ] || fail "failed to parse aarch64 nerdctl archive digest from: ${CONTAINERD_YAML}"

VM_IMAGE_FILE="$(basename "${VM_IMAGE_URL}")"
NERDCTL_FILE="$(basename "${NERDCTL_URL}")"

echo "[1/2] download VM image (aarch64): ${VM_IMAGE_FILE}"
curl -fL -C - -o "${ASSETS_DIR}/${VM_IMAGE_FILE}" "${VM_IMAGE_URL}"

echo "[2/2] download nerdctl archive (aarch64): ${NERDCTL_FILE}"
curl -fL -C - -o "${ASSETS_DIR}/${NERDCTL_FILE}" "${NERDCTL_URL}"

cat >"${ASSETS_DIR}/manifest.json" <<EOF
{
  "schema_version": 1,
  "vm_image": {
    "arch": "aarch64",
    "file": "${VM_IMAGE_FILE}",
    "digest": "${VM_IMAGE_DIGEST}",
    "source_url": "${VM_IMAGE_URL}"
  },
  "containerd_archive": {
    "arch": "aarch64",
    "file": "${NERDCTL_FILE}",
    "digest": "${NERDCTL_DIGEST}",
    "source_url": "${NERDCTL_URL}"
  }
}
EOF

echo "OK: ${ASSETS_DIR}"
