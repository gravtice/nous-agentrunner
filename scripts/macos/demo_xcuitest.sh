#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROJ="${ROOT_DIR}/demo/macos/NousAgentRunnerDemo/NousAgentRunnerDemo.xcodeproj"
ARCH="$(uname -m)"

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

xcodebuild test \
  -project "$PROJ" \
  -scheme NousAgentRunnerDemo \
  -destination "platform=macOS,arch=${ARCH}"
