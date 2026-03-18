#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"

SPM_TMP_ROOT=""
STAGE_DIR=""

fail() {
  echo "error: $*" >&2
  exit 1
}

usage() {
  cat >&2 <<EOF
Usage: $(basename "$0") <app_path>

<app_path> can be:
  - a .app bundle path
  - a directory containing exactly one *.app
  - a SwiftPM package directory (contains Package.swift); script will create a minimal .app wrapper

Env:
  AGENT_RUNNER_CODESIGN_IDENTITY   codesign identity (disables auto-detect)
  AGENT_RUNNER_DISABLE_CODESIGN=1  skip codesign

Auto-detect order (when AGENT_RUNNER_CODESIGN_IDENTITY is unset):
  1) Developer ID Application (notarizable)
  2) Apple Development (Personal Team)
  3) ad-hoc ("-")

Output:
  dist/<AppName>.dmg
EOF
  exit 2
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 not found in PATH"
}

ensure_dir() {
  mkdir -p "$1" || fail "failed to create directory: $1"
}

copy_exec() {
  local src="$1"
  local dst="$2"
  if [ ! -f "$src" ]; then
    fail "missing file: $src"
  fi
  install -m 0755 "$src" "$dst"
}

read_runner_version() {
  local version_file="${ROOT_DIR}/VERSION"
  local v="0.1.0"
  if [ -f "${version_file}" ]; then
    v="$(awk -F= '$1=="AGENT_RUNNER_VERSION"{print $2; exit}' "${version_file}" | tr -d ' \t\r\"')"
  fi
  if [ -z "${v}" ]; then
    v="0.1.0"
  fi
  echo "${v}"
}

maybe_codesign_adhoc() {
  local app="$1"
  if [ "${AGENT_RUNNER_DISABLE_CODESIGN:-}" = "1" ]; then
    return 0
  fi
  if ! command -v codesign >/dev/null 2>&1; then
    return 0
  fi

  local identity=""
  local mode=""
  if [ -n "${AGENT_RUNNER_CODESIGN_IDENTITY:-}" ]; then
    identity="${AGENT_RUNNER_CODESIGN_IDENTITY}"
    mode="explicit"
  elif command -v security >/dev/null 2>&1; then
    identity="$(security find-identity -v -p codesigning 2>/dev/null | awk '/Developer ID Application:/{print $2; exit}')"
    if [ -n "${identity}" ]; then
      mode="developer_id"
    else
      identity="$(security find-identity -v -p codesigning 2>/dev/null | awk '/Apple Development:/{print $2; exit}')"
      if [ -n "${identity}" ]; then
        mode="apple_development"
      else
        identity="-"
        mode="adhoc"
      fi
    fi
  else
    identity="-"
    mode="adhoc"
  fi

  if [ "${mode}" != "adhoc" ] && [ "${mode}" != "explicit" ]; then
    echo "info: auto-selected codesign identity (${mode}): ${identity}" >&2
  fi

  local -a base_flags=(--force --sign "${identity}")
  local -a exec_flags=("${base_flags[@]}" --timestamp=none)
  local -a app_flags=("${base_flags[@]}" --timestamp=none)

  if [ "${mode}" = "developer_id" ]; then
    exec_flags=("${base_flags[@]}" --timestamp --options runtime)
    app_flags=("${base_flags[@]}" --timestamp --options runtime)
  fi

  # Sign injected helper executables (avoid --deep; SwiftPM resource bundles may be minimal dirs).
  local res_dir="${app}/Contents/Resources"
  for f in agent-runnerd guest-runnerd; do
    if [ -f "${res_dir}/${f}" ]; then
      codesign "${exec_flags[@]}" "${res_dir}/${f}" || fail "codesign failed: ${res_dir}/${f}"
    fi
  done
  if [ -f "${res_dir}/limactl" ]; then
    # AVF (vmType=vz) requires com.apple.security.virtualization entitlement on macOS 14+.
    local entitlements="${ROOT_DIR}/references/lima/vz.entitlements"
    if [ -f "$entitlements" ]; then
      codesign "${exec_flags[@]}" --entitlements "$entitlements" "${res_dir}/limactl" || fail "codesign failed: ${res_dir}/limactl"
    else
      fail "missing entitlements file: ${entitlements}"
    fi
  fi

  codesign "${app_flags[@]}" "$app" || fail "codesign failed: $app"
}

build_runtime_binaries_if_needed() {
  ensure_dir "$DIST_DIR"

  if [ "${AGENT_RUNNER_SKIP_BUILD:-}" = "1" ]; then
    if [ ! -f "${DIST_DIR}/agent-runnerd" ]; then
      fail "AGENT_RUNNER_SKIP_BUILD=1 but missing: ${DIST_DIR}/agent-runnerd"
    fi
    if [ ! -f "${DIST_DIR}/guest-runnerd" ]; then
      fail "AGENT_RUNNER_SKIP_BUILD=1 but missing: ${DIST_DIR}/guest-runnerd"
    fi
    for f in limactl; do
      if [ ! -f "${DIST_DIR}/${f}" ]; then
        fail "AGENT_RUNNER_SKIP_BUILD=1 but missing: ${DIST_DIR}/${f}"
      fi
    done
    if [ ! -f "${DIST_DIR}/lima-guestagent.Linux-aarch64" ]; then
      fail "AGENT_RUNNER_SKIP_BUILD=1 but missing: ${DIST_DIR}/lima-guestagent.Linux-aarch64"
    fi
    if [ ! -f "${DIST_DIR}/lima-templates/default.yaml" ]; then
      fail "AGENT_RUNNER_SKIP_BUILD=1 but missing: ${DIST_DIR}/lima-templates/default.yaml"
    fi
    return 0
  fi

  if command -v go >/dev/null 2>&1; then
    "${ROOT_DIR}/scripts/macos/build_binaries.sh"
    return 0
  fi

  # No Go toolchain; fall back to prebuilt binaries in dist/.
  if [ ! -f "${DIST_DIR}/agent-runnerd" ]; then
    fail "go not found in PATH and missing prebuilt binary: ${DIST_DIR}/agent-runnerd"
  fi
  if [ ! -f "${DIST_DIR}/guest-runnerd" ]; then
    fail "go not found in PATH and missing prebuilt binary: ${DIST_DIR}/guest-runnerd"
  fi
  if [ ! -f "${DIST_DIR}/limactl" ]; then
    fail "go not found in PATH and missing prebuilt binary: ${DIST_DIR}/limactl"
  fi
  if [ ! -f "${DIST_DIR}/lima-guestagent.Linux-aarch64" ]; then
    fail "go not found in PATH and missing: ${DIST_DIR}/lima-guestagent.Linux-aarch64"
  fi
  if [ ! -f "${DIST_DIR}/lima-templates/default.yaml" ]; then
    fail "go not found in PATH and missing: ${DIST_DIR}/lima-templates/default.yaml"
  fi
}

pick_single_app_in_dir() {
  local dir="$1"
  local apps
  apps="$(find "$dir" -maxdepth 1 -type d -name "*.app" -print)"
  local count
  count="$(echo "$apps" | sed '/^$/d' | wc -l | tr -d ' ')"
  if [ "$count" = "1" ]; then
    echo "$apps" | head -n 1
    return 0
  fi
  if [ "$count" = "0" ]; then
    return 1
  fi
  fail "multiple .app bundles found under: $dir"
}

create_minimal_app_from_swiftpm() {
  local pkg_dir="$1"
  require_cmd swift
  require_cmd file

  local tmp_root
  tmp_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-runner-app.XXXXXX")"
  SPM_TMP_ROOT="$tmp_root"
  local build_path="${tmp_root}/spm-build"

  local agent_runner_version="0.1.0"
  local version_file="${ROOT_DIR}/VERSION"
  if [ -f "${version_file}" ]; then
    agent_runner_version="$(awk -F= '$1=="AGENT_RUNNER_VERSION"{print $2; exit}' "${version_file}" | tr -d ' \t\r\"')"
    if [ -z "${agent_runner_version}" ]; then
      agent_runner_version="0.1.0"
    fi
  fi

  (cd "$pkg_dir" && swift build -c release --build-path "$build_path" >/dev/null)
  local bin_dir
  bin_dir="$(cd "$pkg_dir" && swift build -c release --show-bin-path --build-path "$build_path")"
  [ -d "$bin_dir" ] || fail "swift build output missing: $bin_dir"

  local exes=()
  while IFS= read -r p; do
    if file "$p" | grep -q "Mach-O.*executable"; then
      exes+=("$p")
    fi
  done < <(find "$bin_dir" -maxdepth 1 -type f -perm -111 -print)

  if [ "${#exes[@]}" -eq 0 ]; then
    fail "no Mach-O executable found under: $bin_dir"
  fi
  if [ "${#exes[@]}" -ne 1 ]; then
    fail "multiple executables found under: $bin_dir (please pass a built .app instead)"
  fi

  local exe_path="${exes[0]}"
  local exe_name
  exe_name="$(basename "$exe_path")"

  local app_dir="${tmp_root}/${exe_name}.app"

  ensure_dir "${app_dir}/Contents/MacOS"
  ensure_dir "${app_dir}/Contents/Resources"

  copy_exec "$exe_path" "${app_dir}/Contents/MacOS/${exe_name}"

  cat >"${app_dir}/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key>
  <string>${exe_name}</string>
  <key>CFBundleDisplayName</key>
  <string>${exe_name}</string>
  <key>CFBundleIdentifier</key>
  <string>ai.gravtice.${exe_name}</string>
  <key>CFBundleExecutable</key>
  <string>${exe_name}</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>${agent_runner_version}</string>
  <key>CFBundleVersion</key>
  <string>${agent_runner_version}</string>
  <key>LSMinimumSystemVersion</key>
  <string>14.0</string>
</dict>
</plist>
EOF

  # Keep SwiftPM resource bundles under Contents/Resources (standard app layout).
  # Note: SwiftPM-generated Bundle.module for executables may look for bundles at the app root.
  # This script does not attempt to rewrite the binary; if you rely on Bundle.module, pass a real .app bundle.
  while IFS= read -r bundle_dir; do
    [ -d "$bundle_dir" ] || continue
    ditto "$bundle_dir" "${app_dir}/Contents/Resources/$(basename "$bundle_dir")"
  done < <(find "$bin_dir" -maxdepth 1 -type d -name "*.bundle" -print)

  # Also copy AgentRunnerConfig.json into the main app resources if provided at the package root.
  # Avoid scanning the whole package directory (e.g. .build artifacts may contain stale copies).
  local cfg_path="${pkg_dir}/AgentRunnerConfig.json"
  if [ -f "$cfg_path" ]; then
    ditto "$cfg_path" "${app_dir}/Contents/Resources/AgentRunnerConfig.json"
  fi

  echo "$app_dir"
}

inject_runtime_into_app() {
  local app="$1"
  local res_dir="${app}/Contents/Resources"
  ensure_dir "$res_dir"

  copy_exec "${DIST_DIR}/agent-runnerd" "${res_dir}/agent-runnerd"
  copy_exec "${DIST_DIR}/guest-runnerd" "${res_dir}/guest-runnerd"
  copy_exec "${DIST_DIR}/limactl" "${res_dir}/limactl"
  local share_dir="${app}/Contents/share/lima"
  ensure_dir "$share_dir"
  copy_exec "${DIST_DIR}/lima-guestagent.Linux-aarch64" "${share_dir}/lima-guestagent.Linux-aarch64"
  if [ ! -d "${DIST_DIR}/lima-templates" ]; then
    fail "missing directory: ${DIST_DIR}/lima-templates"
  fi
  rm -rf "${res_dir}/lima-templates"
  ditto "${DIST_DIR}/lima-templates" "${res_dir}/lima-templates"

  local runner_version
  runner_version="$(read_runner_version)"
  local default_image="docker.io/gravtice/agent-runner-claude-agent-service:${runner_version}"

  # Optional: bundle offline assets to avoid first-run downloads.
  # See: scripts/macos/fetch_offline_assets.sh
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

  # Single source of truth: runtime-manifest.json (no extra manifests in the app bundle).
  require_cmd python3
  AGENT_RUNNER_VERSION="${runner_version}" \
    AGENT_RUNNER_DEFAULT_IMAGE_REF="${default_image}" \
    AGENT_RUNNER_OFFLINE_ASSETS_DIR="${offline_dir}" \
    AGENT_RUNNER_OFFLINE_ASSETS_MANIFEST="${offline_manifest}" \
    python3 - <<PY >"${res_dir}/runtime-manifest.json"
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
  require_cmd hdiutil
  require_cmd ditto

  local input="${1:-}"
  if [ -z "$input" ]; then
    usage
  fi
  if [ ! -e "$input" ]; then
    fail "path not found: $input"
  fi

  build_runtime_binaries_if_needed

  local app_src=""
  if [ -d "$input" ] && [[ "$input" == *.app ]]; then
    app_src="$input"
  elif [ -d "$input" ]; then
    if app_src="$(pick_single_app_in_dir "$input")"; then
      :
    elif [ -f "${input}/Package.swift" ]; then
      app_src="$(create_minimal_app_from_swiftpm "$input")"
    else
      fail "no .app found (please pass a .app bundle path or a dir containing one): $input"
    fi
  else
    fail "input must be a directory: $input"
  fi

  local app_name
  app_name="$(basename "$app_src")"
  local app_base="${app_name%.app}"

  STAGE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/agent-runner-dmg.XXXXXX")"
  trap 'rm -rf "${STAGE_DIR}"; rm -rf "${SPM_TMP_ROOT}"' EXIT

  local dst_app="${STAGE_DIR}/${app_name}"
  ditto "$app_src" "$dst_app"
  inject_runtime_into_app "$dst_app"
  maybe_codesign_adhoc "$dst_app"

  ln -s /Applications "${STAGE_DIR}/Applications"

  ensure_dir "$DIST_DIR"
  local dmg_path="${DIST_DIR}/${app_base}.dmg"
  rm -f "$dmg_path"

  hdiutil create \
    -volname "$app_base" \
    -srcfolder "$STAGE_DIR" \
    -ov -format UDZO \
    "$dmg_path" >/dev/null

  echo "OK: $dmg_path"
}

main "$@"
