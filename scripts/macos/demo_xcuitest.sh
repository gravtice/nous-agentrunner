#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROJ="${ROOT_DIR}/demo/macos/AgentRunnerDemo/AgentRunnerDemo.xcodeproj"
ARCH="$(uname -m)"
DIST_DIR="${ROOT_DIR}/dist"

fail() {
  echo "error: $*" >&2
  exit 1
}

read_runner_version() {
  awk -F= '$1=="AGENT_RUNNER_VERSION"{print $2; exit}' "${ROOT_DIR}/VERSION"
}

ensure_demo_service_env() {
  local existing=""
  local lines=()
  local service_env=""

  if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
    lines+=("ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}")
  fi
  if [ -n "${ANTHROPIC_AUTH_TOKEN:-}" ]; then
    lines+=("ANTHROPIC_AUTH_TOKEN=${ANTHROPIC_AUTH_TOKEN}")
  fi
  if [ -n "${ANTHROPIC_BASE_URL:-}" ]; then
    lines+=("ANTHROPIC_BASE_URL=${ANTHROPIC_BASE_URL}")
  fi
  if [ -n "${ANTHROPIC_MODEL:-}" ]; then
    lines+=("ANTHROPIC_MODEL=${ANTHROPIC_MODEL}")
  fi
  if [ "${#lines[@]}" -gt 0 ] && { [ -n "${ANTHROPIC_API_KEY:-}" ] || [ -n "${ANTHROPIC_AUTH_TOKEN:-}" ]; }; then
    service_env="$(printf '%s\n' "${lines[@]}")"
    defaults write ai.gravtice.AgentRunnerDemo agent_runner.demo.service_env -string "${service_env}"
    return
  fi

  existing="$(defaults read ai.gravtice.AgentRunnerDemo agent_runner.demo.service_env 2>/dev/null || true)"
  existing="${existing#"${existing%%[![:space:]]*}"}"
  existing="${existing%"${existing##*[![:space:]]}"}"
  if [ -n "${existing}" ]; then
    return
  fi

  fail "missing Claude credentials: set ai.gravtice.AgentRunnerDemo agent_runner.demo.service_env or export ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN"
}

ensure_local_service_image() {
  local runner_version image_ref offline_dir offline_manifest image_file src_ref=""
  runner_version="$(read_runner_version)"
  if [ -z "${runner_version}" ]; then
    fail "failed to read runner version"
  fi
  image_ref="docker.io/gravtice/claude-agent-service:${runner_version}"
  offline_dir="${DIST_DIR}/offline-assets"
  offline_manifest="${offline_dir}/manifest.json"
  image_file="images/claude-agent-service-${runner_version}.tar"

  if [ ! -f "${offline_manifest}" ]; then
    if ! command -v docker >/dev/null 2>&1; then
      fail "docker is required to prepare demo offline assets"
    fi
    "${ROOT_DIR}/scripts/macos/fetch_offline_assets.sh"
  fi

  if [ -f "${offline_dir}/${image_file}" ] && python3 - <<'PY' "${offline_manifest}" "${image_ref}" "${image_file}"
import json
import sys

manifest_path, image_ref, image_file = sys.argv[1:4]
with open(manifest_path, "r", encoding="utf-8") as f:
    manifest = json.load(f)
for item in manifest.get("images", []):
    if item.get("ref") == image_ref and item.get("file") == image_file:
        raise SystemExit(0)
raise SystemExit(1)
PY
  then
    return
  fi

  if ! command -v docker >/dev/null 2>&1; then
    fail "docker is required to export claude-agent-service offline image"
  fi

  for cand in \
    "${image_ref}" \
    "gravtice/claude-agent-service:${runner_version}" \
    "claude-agent-service:${runner_version}" \
    "local/claude-agent-service:${runner_version}"
  do
    if docker image inspect "${cand}" >/dev/null 2>&1; then
      src_ref="${cand}"
      break
    fi
  done

  if [ -z "${src_ref}" ]; then
    docker build -f "${ROOT_DIR}/services/claude-agent-service/Dockerfile" -t "${image_ref}" "${ROOT_DIR}"
    src_ref="${image_ref}"
  elif [ "${src_ref}" != "${image_ref}" ]; then
    docker tag "${src_ref}" "${image_ref}"
  fi

  mkdir -p "${offline_dir}/images"
  docker save -o "${offline_dir}/${image_file}" "${image_ref}"

  python3 - <<'PY' "${offline_manifest}" "${image_ref}" "${image_file}"
import json
import sys

manifest_path, image_ref, image_file = sys.argv[1:4]
with open(manifest_path, "r", encoding="utf-8") as f:
    manifest = json.load(f)

images = [
    item for item in manifest.get("images", [])
    if item.get("ref") != image_ref
]
images.append({"ref": image_ref, "file": image_file})
manifest["images"] = images

with open(manifest_path, "w", encoding="utf-8") as f:
    json.dump(manifest, f, indent=2)
    f.write("\n")
PY
}

if command -v osascript >/dev/null 2>&1; then
  osascript -l JavaScript >/dev/null 2>&1 <<'JXA' || true
ObjC.import("Carbon")

function inputSourceID(source) {
  if (!source) return ""
  var idRef = $.TISGetInputSourceProperty(source, $.kTISPropertyInputSourceID)
  if (!idRef) return ""
  return ObjC.unwrap(ObjC.castRefToObject(idRef))
}

var ascii = $.TISCopyCurrentASCIICapableKeyboardInputSource()
if (!ascii) $.exit(0)

var before = inputSourceID($.TISCopyCurrentKeyboardInputSource())
$.TISSelectInputSource(ascii)

var after = inputSourceID($.TISCopyCurrentKeyboardInputSource())
if (after && after !== before) {
  $.exit(0)
}
JXA
fi

if [ ! -d "$PROJ" ]; then
  echo "error: missing Xcode project: $PROJ" >&2
  exit 1
fi

ensure_demo_service_env
ensure_local_service_image

xcodebuild test \
  -project "$PROJ" \
  -scheme AgentRunnerDemo \
  -destination "platform=macOS,arch=${ARCH}"
