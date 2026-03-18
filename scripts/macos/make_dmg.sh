#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"

APP_PATH="${DIST_DIR}/AgentRunnerDemo.app"
DMG_PATH="${DIST_DIR}/AgentRunnerDemo.dmg"

if [ ! -d "${APP_PATH}" ]; then
  echo "missing app bundle: ${APP_PATH}" >&2
  echo "build the demo app first (Xcode recommended), then place it under dist/." >&2
  exit 1
fi

if ! command -v hdiutil >/dev/null 2>&1; then
  echo "hdiutil not found (run on macOS)" >&2
  exit 1
fi

rm -f "${DMG_PATH}"
hdiutil create \
  -volname "Agent Runner Demo" \
  -srcfolder "${APP_PATH}" \
  -ov -format UDZO \
  "${DMG_PATH}"

echo "OK: ${DMG_PATH}"

