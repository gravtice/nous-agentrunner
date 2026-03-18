#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"

fail() {
  echo "error: $*" >&2
  exit 1
}

ensure_dist() {
  mkdir -p "${DIST_DIR}"
  if command -v go >/dev/null 2>&1; then
    "${ROOT_DIR}/scripts/macos/build_binaries.sh"
    return
  fi

  for f in agent-runnerd guest-runnerd limactl lima-guestagent.Linux-aarch64; do
    if [ ! -f "${DIST_DIR}/${f}" ]; then
      fail "go not found and missing: ${DIST_DIR}/${f}"
    fi
  done
  if [ ! -d "${DIST_DIR}/lima-templates" ]; then
    fail "go not found and missing directory: ${DIST_DIR}/lima-templates"
  fi
}

read_runner_version() {
  awk -F= '$1=="AGENT_RUNNER_VERSION"{print $2; exit}' "${ROOT_DIR}/VERSION"
}

copy_exec() {
  local src="$1"
  local dst="$2"
  install -m 0755 "${src}" "${dst}"
}

codesign_execs() {
  local res_dir="$1"
  if ! command -v codesign >/dev/null 2>&1; then
    return
  fi

  codesign --force --sign - --timestamp=none "${res_dir}/agent-runnerd" >/dev/null
  codesign --force --sign - --timestamp=none "${res_dir}/guest-runnerd" >/dev/null

  local entitlements="${ROOT_DIR}/references/lima/vz.entitlements"
  if [ ! -f "${entitlements}" ]; then
    fail "missing entitlements file: ${entitlements}"
  fi
  codesign --force --sign - --timestamp=none --entitlements "${entitlements}" "${res_dir}/limactl" >/dev/null
}

write_runtime_manifest() {
  local res_dir="$1"
  local runner_version="$2"
  local default_image="docker.io/gravtice/claude-agent-service:${runner_version}"
  local offline_dir=""
  local offline_manifest=""

  if [ -d "${DIST_DIR}/offline-assets" ]; then
    offline_dir="agent-runner-offline-assets"
    offline_manifest="${DIST_DIR}/offline-assets/manifest.json"
    if [ ! -f "${offline_manifest}" ]; then
      fail "offline-assets present but missing manifest.json: ${DIST_DIR}/offline-assets"
    fi
    rm -rf "${res_dir}/agent-runner-offline-assets"
    ditto "${DIST_DIR}/offline-assets" "${res_dir}/agent-runner-offline-assets"
    rm -f "${res_dir}/agent-runner-offline-assets/manifest.json"
  fi

  AGENT_RUNNER_VERSION="${runner_version}" \
    AGENT_RUNNER_DEFAULT_IMAGE_REF="${default_image}" \
    AGENT_RUNNER_OFFLINE_ASSETS_DIR="${offline_dir}" \
    AGENT_RUNNER_OFFLINE_ASSETS_MANIFEST="${offline_manifest}" \
    python3 - <<'PY' >"${res_dir}/runtime-manifest.json"
import json
import os

runner_version = os.environ["AGENT_RUNNER_VERSION"]
default_image = os.environ["AGENT_RUNNER_DEFAULT_IMAGE_REF"]
offline_dir = os.environ.get("AGENT_RUNNER_OFFLINE_ASSETS_DIR", "").strip()
offline_manifest = os.environ.get("AGENT_RUNNER_OFFLINE_ASSETS_MANIFEST", "").strip()

m = {
    "schema_version": 1,
    "runner_version": runner_version,
    "image_contract_version": 1,
    "default_images": {
        "claude_agent_service": default_image,
    },
}

if offline_dir and offline_manifest:
    with open(offline_manifest, "r", encoding="utf-8") as f:
        src = json.load(f)
    if src.get("schema_version") != 1:
        raise SystemExit(f"unsupported offline-assets schema_version={src.get('schema_version')}")
    m["offline_assets"] = {
        "dir": offline_dir,
        "vm_image": src.get("vm_image", {}),
        "containerd_archive": src.get("containerd_archive", {}),
        "images": src.get("images", []),
    }

print(json.dumps(m, indent=2))
PY

  python3 -m json.tool "${res_dir}/runtime-manifest.json" >/dev/null || fail "runtime-manifest.json is not valid JSON"
}

main() {
  : "${TARGET_BUILD_DIR:?TARGET_BUILD_DIR is required}"
  : "${UNLOCALIZED_RESOURCES_FOLDER_PATH:?UNLOCALIZED_RESOURCES_FOLDER_PATH is required}"
  : "${CONTENTS_FOLDER_PATH:?CONTENTS_FOLDER_PATH is required}"

  ensure_dist

  local res_dir="${TARGET_BUILD_DIR}/${UNLOCALIZED_RESOURCES_FOLDER_PATH}"
  local contents_dir="${TARGET_BUILD_DIR}/${CONTENTS_FOLDER_PATH}"
  local share_dir="${contents_dir}/share/lima"
  local runner_version
  runner_version="$(read_runner_version)"
  if [ -z "${runner_version}" ]; then
    fail "failed to read runner version"
  fi

  mkdir -p "${res_dir}" "${share_dir}"

  copy_exec "${DIST_DIR}/agent-runnerd" "${res_dir}/agent-runnerd"
  copy_exec "${DIST_DIR}/guest-runnerd" "${res_dir}/guest-runnerd"
  copy_exec "${DIST_DIR}/limactl" "${res_dir}/limactl"
  copy_exec "${DIST_DIR}/lima-guestagent.Linux-aarch64" "${share_dir}/lima-guestagent.Linux-aarch64"

  rm -rf "${res_dir}/lima-templates"
  ditto "${DIST_DIR}/lima-templates" "${res_dir}/lima-templates"

  write_runtime_manifest "${res_dir}" "${runner_version}"
  codesign_execs "${res_dir}"
}

main "$@"
