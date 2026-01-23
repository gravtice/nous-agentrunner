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

VM_LINE="$(
  awk '
    function flush() {
      if (loc != "" && (arch == "aarch64" || arch == "arm64")) {
        print loc, dig
        exit
      }
    }
    $1=="-" && $2=="location:" {flush(); loc=$3; gsub(/"/,"",loc); arch=""; dig=""; next}
    $1=="arch:" {arch=$2; gsub(/"/,"",arch); next}
    $1=="digest:" {dig=$2; gsub(/"/,"",dig); next}
    END {flush()}
  ' "${DEBIAN_YAML}"
)"
[ -n "${VM_LINE:-}" ] || fail "failed to parse arm64/aarch64 VM image from: ${DEBIAN_YAML}"
VM_IMAGE_URL="${VM_LINE%% *}"
VM_IMAGE_DIGEST="${VM_LINE#* }"
if [ "${VM_IMAGE_DIGEST}" = "${VM_IMAGE_URL}" ]; then
  VM_IMAGE_DIGEST=""
fi

NERDCTL_LINE="$(
  awk '
    function flush() {
      if (loc != "" && (arch == "aarch64" || arch == "arm64")) {
        print loc, dig
        exit
      }
    }
    $1=="-" && $2=="location:" {flush(); loc=$3; gsub(/"/,"",loc); arch=""; dig=""; next}
    $1=="arch:" {arch=$2; gsub(/"/,"",arch); next}
    $1=="digest:" {dig=$2; gsub(/"/,"",dig); next}
    END {flush()}
  ' "${CONTAINERD_YAML}"
)"
[ -n "${NERDCTL_LINE:-}" ] || fail "failed to parse arm64/aarch64 nerdctl archive from: ${CONTAINERD_YAML}"
NERDCTL_URL="${NERDCTL_LINE%% *}"
NERDCTL_DIGEST="${NERDCTL_LINE#* }"
if [ "${NERDCTL_DIGEST}" = "${NERDCTL_URL}" ]; then
  NERDCTL_DIGEST=""
fi

echo "VM image: ${VM_IMAGE_URL}"
if [ -n "${VM_IMAGE_DIGEST}" ]; then
  echo "VM digest: ${VM_IMAGE_DIGEST}"
else
  echo "VM digest: (missing)"
fi
echo "nerdctl archive: ${NERDCTL_URL}"
if [ -n "${NERDCTL_DIGEST}" ]; then
  echo "nerdctl digest: ${NERDCTL_DIGEST}"
else
  echo "nerdctl digest: (missing)"
fi

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
