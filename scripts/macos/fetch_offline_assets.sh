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
if command -v docker >/dev/null 2>&1; then
  :
else
  fail "docker not found in PATH (required for qcow2 pre-bake)"
fi

mkdir -p "${ASSETS_DIR}"

VERSION_FILE="${ROOT_DIR}/VERSION"
[ -f "${VERSION_FILE}" ] || fail "missing: ${VERSION_FILE}"
AGENT_RUNNER_VERSION="$(awk -F= '$1=="AGENT_RUNNER_VERSION"{print $2; exit}' "${VERSION_FILE}" | tr -d ' \t\r\"')"
AGENT_RUNNER_VM_VERSION="$(awk -F= '$1=="AGENT_RUNNER_VM_VERSION"{print $2; exit}' "${VERSION_FILE}" | tr -d ' \t\r\"')"
if [ -z "${AGENT_RUNNER_VERSION}" ]; then
  fail "missing AGENT_RUNNER_VERSION in ${VERSION_FILE}"
fi
if [ -z "${AGENT_RUNNER_VM_VERSION}" ]; then
  fail "missing AGENT_RUNNER_VM_VERSION in ${VERSION_FILE}"
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
VM_IMAGE_SOURCE_DIGEST="${VM_LINE#* }"
if [ "${VM_IMAGE_SOURCE_DIGEST}" = "${VM_IMAGE_URL}" ]; then
  VM_IMAGE_SOURCE_DIGEST=""
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
if [ -n "${VM_IMAGE_SOURCE_DIGEST}" ]; then
  echo "VM digest: ${VM_IMAGE_SOURCE_DIGEST}"
else
  echo "VM digest: (missing)"
fi
echo "nerdctl archive: ${NERDCTL_URL}"
if [ -n "${NERDCTL_DIGEST}" ]; then
  echo "nerdctl digest: ${NERDCTL_DIGEST}"
else
  echo "nerdctl digest: (missing)"
fi

case "${VM_IMAGE_URL}${VM_IMAGE_SOURCE_DIGEST}${NERDCTL_URL}${NERDCTL_DIGEST}" in
  *$'\n'* | *$'\r'*)
    fail "parsed values contain invalid newline characters"
    ;;
esac

VM_IMAGE_FILE="$(basename "${VM_IMAGE_URL}")"
NERDCTL_FILE="$(basename "${NERDCTL_URL}")"

if [ "${VM_IMAGE_FILE}" != "${AGENT_RUNNER_VM_VERSION}" ]; then
  fail "VM image filename mismatch: VERSION has ${AGENT_RUNNER_VM_VERSION} but template resolves ${VM_IMAGE_FILE}"
fi

sha_digest() {
  local algo="$1"
  local path="$2"
  python3 - "$algo" "$path" <<'PY'
import hashlib
import sys

algo = sys.argv[1].strip().lower()
path = sys.argv[2]
try:
    h = hashlib.new(algo)
except ValueError as e:
    raise SystemExit(f"unsupported digest algorithm: {algo}") from e

with open(path, "rb") as f:
    for chunk in iter(lambda: f.read(1024 * 1024), b""):
        h.update(chunk)

print(f"{algo}:{h.hexdigest()}")
PY
}

bake_vm_image() {
  local input="$1"
  local output="$2"

  if [ "$(dirname "${input}")" != "${ASSETS_DIR}" ] || [ "$(dirname "${output}")" != "${ASSETS_DIR}" ]; then
    fail "bake_vm_image expects input/output under ${ASSETS_DIR} (got: ${input}, ${output})"
  fi

  local baker_image="agent-runner-offline-baker:bookworm-v2"
  if ! docker image inspect "${baker_image}" >/dev/null 2>&1; then
    echo "[bake] build ${baker_image} (libguestfs-tools)"
    docker build -t "${baker_image}" - <<'EOF'
FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    ca-certificates \
    ipxe-qemu \
    libguestfs-tools \
    linux-image-arm64 \
  && rm -rf /var/lib/apt/lists/*
RUN ln -sf /usr/lib/ipxe/qemu/efi-virtio.rom /usr/share/qemu/efi-virtio.rom
EOF
  fi

  local tmp_script
  tmp_script="$(mktemp "${ASSETS_DIR}/.agent-runner-vm-bake.XXXXXX")"
  trap 'rm -f "${tmp_script}"' RETURN
  cat >"${tmp_script}" <<'SH'
#!/bin/sh
set -eux

pkgs=""
if [ ! -e /usr/sbin/iptables ]; then
	pkgs="${pkgs} iptables"
fi
if ! command -v rsync >/dev/null 2>&1; then
	pkgs="${pkgs} rsync"
fi

if [ -n "${pkgs}" ]; then
	DEBIAN_FRONTEND=noninteractive
	export DEBIAN_FRONTEND
	apt-get update
	# shellcheck disable=SC2086
	apt-get install -y --no-upgrade --no-install-recommends -q ${pkgs}
fi

apt-get clean
rm -rf /var/lib/apt/lists/* /var/cache/apt/archives/*
rm -rf /tmp/* /var/tmp/*
SH

  local tmp_out
  local tmp_out_base_path
  tmp_out_base_path="$(mktemp "${ASSETS_DIR}/.agent-runner-vm-image.XXXXXX")"
  tmp_out="${tmp_out_base_path}.qcow2"
  rm -f "${tmp_out_base_path}"
  rm -f "${tmp_out}"
  trap 'rm -f "${tmp_script}" "${tmp_out_base_path}" "${tmp_out}"' RETURN

  local input_base
  input_base="$(basename "${input}")"
  local tmp_script_base
  tmp_script_base="$(basename "${tmp_script}")"
  local tmp_out_file
  tmp_out_file="$(basename "${tmp_out}")"

  echo "[bake] virt-customize ${input_base}"
  docker run --rm \
    -e LIBGUESTFS_BACKEND_SETTINGS=force_tcg \
    -v "${ASSETS_DIR}:/assets" \
    "${baker_image}" \
    virt-customize -a "/assets/${input_base}" --run "/assets/${tmp_script_base}"

  echo "[bake] virt-sparsify ${input_base} -> $(basename "${output}")"
  docker run --rm \
    -e LIBGUESTFS_BACKEND_SETTINGS=force_tcg \
    -v "${ASSETS_DIR}:/assets" \
    "${baker_image}" \
    virt-sparsify --compress "/assets/${input_base}" "/assets/${tmp_out_file}"

  rm -f "${output}"
  mv -f "${tmp_out}" "${output}"
  trap - RETURN
  rm -f "${tmp_script}"
}

digest_algo() {
  local digest="$1"
  if [ -z "${digest}" ]; then
    return 0
  fi
  echo "${digest%%:*}"
}

verify_digest() {
  local path="$1"
  local expected="$2"
  if [ -z "${expected}" ]; then
    return 0
  fi
  local algo
  algo="$(digest_algo "${expected}" | tr '[:upper:]' '[:lower:]')"
  if [ -z "${algo}" ] || [ "${algo}" = "${expected}" ]; then
    fail "invalid digest format: ${expected}"
  fi
  local got
  got="$(sha_digest "${algo}" "${path}")"
  local expected_hex="${expected#*:}"
  local expected_norm="${algo}:${expected_hex}"
  if [ "${got}" != "${expected_norm}" ]; then
    fail "digest mismatch for ${path}: expected ${expected_norm} but got ${got}"
  fi
}

VM_IMAGE_PATH="${ASSETS_DIR}/${VM_IMAGE_FILE}"
VM_IMAGE_DOWNLOAD_PATH="${ASSETS_DIR}/${VM_IMAGE_FILE}.download"

if [ -f "${VM_IMAGE_PATH}" ]; then
  echo "[1/2] VM image already present: ${VM_IMAGE_FILE}"
  if [ -n "${VM_IMAGE_SOURCE_DIGEST}" ]; then
    source_algo="$(digest_algo "${VM_IMAGE_SOURCE_DIGEST}" | tr '[:upper:]' '[:lower:]')"
    existing_digest="$(sha_digest "${source_algo}" "${VM_IMAGE_PATH}")"
    expected_hex="${VM_IMAGE_SOURCE_DIGEST#*:}"
    expected_norm="${source_algo}:${expected_hex}"
    if [ "${existing_digest}" = "${expected_norm}" ]; then
      echo "[1/2] bake existing VM image (preinstall iptables, rsync): ${VM_IMAGE_FILE}"
      bake_vm_image "${VM_IMAGE_PATH}" "${VM_IMAGE_PATH}"
    fi
  fi
else
  if [ ! -f "${VM_IMAGE_DOWNLOAD_PATH}" ]; then
    echo "[1/2] download VM image (aarch64): ${VM_IMAGE_FILE}"
    curl -fL -C - -o "${VM_IMAGE_DOWNLOAD_PATH}" "${VM_IMAGE_URL}"
  else
    echo "[1/2] VM image download present: $(basename "${VM_IMAGE_DOWNLOAD_PATH}")"
  fi
  verify_digest "${VM_IMAGE_DOWNLOAD_PATH}" "${VM_IMAGE_SOURCE_DIGEST}"

  echo "[1/2] bake VM image (preinstall iptables, rsync): ${VM_IMAGE_FILE}"
  bake_vm_image "${VM_IMAGE_DOWNLOAD_PATH}" "${VM_IMAGE_PATH}"
  rm -f "${VM_IMAGE_DOWNLOAD_PATH}"
fi

VM_IMAGE_DIGEST="$(sha_digest sha512 "${VM_IMAGE_PATH}")"

echo "[2/2] download nerdctl archive (aarch64): ${NERDCTL_FILE}"
curl -fL -C - -o "${ASSETS_DIR}/${NERDCTL_FILE}" "${NERDCTL_URL}"
verify_digest "${ASSETS_DIR}/${NERDCTL_FILE}" "${NERDCTL_DIGEST}"
if [ -z "${NERDCTL_DIGEST}" ]; then
  NERDCTL_DIGEST="$(sha_digest sha256 "${ASSETS_DIR}/${NERDCTL_FILE}")"
fi

OFFLINE_IMAGE_REF=""
OFFLINE_IMAGE_FILE=""
if command -v docker >/dev/null 2>&1; then
  # KISS: bundle the default claude agent service image that matches the main version.
  OFFLINE_IMAGE_REF="docker.io/gravtice/agent-runner-claude-agent-service:${AGENT_RUNNER_VERSION}"
  src_ref=""
  for cand in \
    "${OFFLINE_IMAGE_REF}" \
    "gravtice/agent-runner-claude-agent-service:${AGENT_RUNNER_VERSION}" \
    "agent-runner-claude-agent-service:${AGENT_RUNNER_VERSION}" \
    "local/agent-runner-claude-agent-service:${AGENT_RUNNER_VERSION}"
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
    OFFLINE_IMAGE_FILE="images/agent-runner-claude-agent-service-${AGENT_RUNNER_VERSION}.tar"
    echo "[image] export ${OFFLINE_IMAGE_REF} -> ${OFFLINE_IMAGE_FILE}"
    docker save -o "${ASSETS_DIR}/${OFFLINE_IMAGE_FILE}" "${OFFLINE_IMAGE_REF}"
  else
    echo "[image] skip: local image not found for ${OFFLINE_IMAGE_REF}"
    OFFLINE_IMAGE_REF=""
    OFFLINE_IMAGE_FILE=""
  fi
fi

export AGENT_RUNNER_VERSION AGENT_RUNNER_VM_VERSION VM_IMAGE_FILE VM_IMAGE_DIGEST VM_IMAGE_URL NERDCTL_FILE NERDCTL_DIGEST NERDCTL_URL OFFLINE_IMAGE_REF OFFLINE_IMAGE_FILE
python3 - <<'PY' >"${ASSETS_DIR}/manifest.json"
import json
import os

m = {
    "schema_version": 1,
    "agent_runner_version": os.environ["AGENT_RUNNER_VERSION"],
    "agent_runner_vm_version": os.environ["AGENT_RUNNER_VM_VERSION"],
    "vm_image": {
        "arch": "aarch64",
        "file": os.environ["VM_IMAGE_FILE"],
        "digest": os.environ.get("VM_IMAGE_DIGEST", ""),
        "source_url": os.environ["VM_IMAGE_URL"],
        "baked": True,
        "baked_packages": ["iptables", "rsync"],
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
