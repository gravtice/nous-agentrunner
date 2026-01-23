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
require_cmd python3

mkdir -p "${ASSETS_DIR}"

VERSION_FILE="${ROOT_DIR}/VERSION"
[ -f "${VERSION_FILE}" ] || fail "missing: ${VERSION_FILE}"
NOUS_VERSION="$(awk -F= '$1=="NOUS_VERSION"{print $2; exit}' "${VERSION_FILE}" | tr -d ' \t\r\"')"
NOUS_VM_VERSION="$(awk -F= '$1=="NOUS_VM_VERSION"{print $2; exit}' "${VERSION_FILE}" | tr -d ' \t\r\"')"
if [ -z "${NOUS_VERSION}" ]; then
  fail "missing NOUS_VERSION in ${VERSION_FILE}"
fi
if [ -z "${NOUS_VM_VERSION}" ]; then
  fail "missing NOUS_VM_VERSION in ${VERSION_FILE}"
fi

DEBIAN_YAML="${ROOT_DIR}/references/lima/templates/_images/debian-12.yaml"
CONTAINERD_YAML="${ROOT_DIR}/references/lima/pkg/limayaml/containerd.yaml"
[ -f "${DEBIAN_YAML}" ] || fail "missing: ${DEBIAN_YAML}"
[ -f "${CONTAINERD_YAML}" ] || fail "missing: ${CONTAINERD_YAML}"

VM_LINE="$(
  awk '
    function flush() {
      if (!found && loc != "" && (arch == "aarch64" || arch == "arm64")) {
        print loc, dig
        found = 1
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
      if (!found && loc != "" && (arch == "aarch64" || arch == "arm64")) {
        print loc, dig
        found = 1
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

case "${VM_IMAGE_URL}${VM_IMAGE_DIGEST}${NERDCTL_URL}${NERDCTL_DIGEST}" in
  *$'\n'* | *$'\r'*)
    fail "parsed values contain invalid newline characters"
    ;;
esac

VM_IMAGE_FILE="$(basename "${VM_IMAGE_URL}")"
NERDCTL_FILE="$(basename "${NERDCTL_URL}")"

if [ "${VM_IMAGE_FILE}" != "${NOUS_VM_VERSION}" ]; then
  fail "VM image filename mismatch: VERSION has ${NOUS_VM_VERSION} but template resolves ${VM_IMAGE_FILE}"
fi

echo "[1/2] download VM image (aarch64): ${VM_IMAGE_FILE}"
curl -fL -C - -o "${ASSETS_DIR}/${VM_IMAGE_FILE}" "${VM_IMAGE_URL}"

echo "[2/2] download nerdctl archive (aarch64): ${NERDCTL_FILE}"
curl -fL -C - -o "${ASSETS_DIR}/${NERDCTL_FILE}" "${NERDCTL_URL}"

OFFLINE_IMAGE_REF=""
OFFLINE_IMAGE_FILE=""
if command -v docker >/dev/null 2>&1; then
  # KISS: bundle the default claude agent service image that matches the main version.
  OFFLINE_IMAGE_REF="docker.io/gravtice/nous-claude-agent-service:${NOUS_VERSION}"
  src_ref=""
  for cand in \
    "${OFFLINE_IMAGE_REF}" \
    "gravtice/nous-claude-agent-service:${NOUS_VERSION}" \
    "nous-claude-agent-service:${NOUS_VERSION}" \
    "local/nous-claude-agent-service:${NOUS_VERSION}"
  do
    if docker image inspect "${cand}" >/dev/null 2>&1; then
      src_ref="${cand}"
      break
    fi
  done
  if [ -n "${src_ref}" ]; then
    if [ "${src_ref}" != "${OFFLINE_IMAGE_REF}" ]; then
      docker tag "${src_ref}" "${OFFLINE_IMAGE_REF}"
    fi
    mkdir -p "${ASSETS_DIR}/images"
    OFFLINE_IMAGE_FILE="images/nous-claude-agent-service-${NOUS_VERSION}.tar"
    echo "[image] export ${OFFLINE_IMAGE_REF} -> ${OFFLINE_IMAGE_FILE}"
    docker save -o "${ASSETS_DIR}/${OFFLINE_IMAGE_FILE}" "${OFFLINE_IMAGE_REF}"
  else
    echo "[image] skip: local image not found for ${OFFLINE_IMAGE_REF}"
    OFFLINE_IMAGE_REF=""
    OFFLINE_IMAGE_FILE=""
  fi
fi

export NOUS_VERSION NOUS_VM_VERSION VM_IMAGE_FILE VM_IMAGE_DIGEST VM_IMAGE_URL NERDCTL_FILE NERDCTL_DIGEST NERDCTL_URL OFFLINE_IMAGE_REF OFFLINE_IMAGE_FILE
python3 - <<'PY' >"${ASSETS_DIR}/manifest.json"
import json
import os

m = {
    "schema_version": 1,
    "nous_version": os.environ["NOUS_VERSION"],
    "nous_vm_version": os.environ["NOUS_VM_VERSION"],
    "vm_image": {
        "arch": "aarch64",
        "file": os.environ["VM_IMAGE_FILE"],
        "digest": os.environ.get("VM_IMAGE_DIGEST", ""),
        "source_url": os.environ["VM_IMAGE_URL"],
    },
    "containerd_archive": {
        "arch": "aarch64",
        "file": os.environ["NERDCTL_FILE"],
        "digest": os.environ.get("NERDCTL_DIGEST", ""),
        "source_url": os.environ["NERDCTL_URL"],
    },
}

ref = os.environ.get("OFFLINE_IMAGE_REF", "").strip()
file = os.environ.get("OFFLINE_IMAGE_FILE", "").strip()
if ref and file:
    m["images"] = [{"ref": ref, "file": file}]

print(json.dumps(m, indent=2))
PY

python3 -m json.tool "${ASSETS_DIR}/manifest.json" >/dev/null || fail "manifest.json is not valid JSON"

echo "OK: ${ASSETS_DIR}"
