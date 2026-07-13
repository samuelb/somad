#!/usr/bin/env bash
#
# Build a distributable .dmg around the universal soma binary. Unlike whirr,
# soma is a CLI-first tool, so the image carries the raw binary (plus docs)
# rather than an .app bundle -- users copy "soma" somewhere on their PATH.
#
# Usage:
#   packaging/macos/build-dmg.sh <path-to-universal-soma-binary> [output-dir]
#
set -euo pipefail

BIN="${1:?path to soma binary required}"
OUT_DIR="${2:-dist}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

DMG="${OUT_DIR}/soma-macos.dmg"
mkdir -p "${OUT_DIR}"
rm -f "${DMG}"

STAGE="$(mktemp -d)"
trap 'rm -rf "${STAGE}"' EXIT

install -m 0755 "${BIN}" "${STAGE}/soma"
cp "${ROOT}/LICENSE" "${ROOT}/README.md" "${STAGE}/"

hdiutil create -volname "Soma" -srcfolder "${STAGE}" -ov -format UDZO "${DMG}"
echo "Built ${DMG}"
