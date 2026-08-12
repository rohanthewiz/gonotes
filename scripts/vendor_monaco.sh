#!/bin/bash
# Vendor the Monaco editor into web/static/vendor/monaco so the note-body
# editor works without internet connectivity.
#
# The files are embedded into the gonotes binary via go:embed (web/static.go),
# so run this script and rebuild to pick up a new Monaco version. Keep the
# version here in sync with MONACO_VERSION in web/static/js/monaco_editor.js —
# the client prefers the vendored copy and only falls back to the CDN (pinned
# to that same version) when the vendored files are missing.
#
# We pull the npm tarball directly rather than using npm itself so the script
# has no toolchain dependency beyond curl + tar.

set -euo pipefail

MONACO_VERSION="0.52.2"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VENDOR_DIR="$SCRIPT_DIR/../web/static/vendor/monaco"
TARBALL_URL="https://registry.npmjs.org/monaco-editor/-/monaco-editor-${MONACO_VERSION}.tgz"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading monaco-editor@${MONACO_VERSION}..."
curl -sSL "$TARBALL_URL" -o "$TMP_DIR/monaco.tgz"

echo "Extracting min build (min/vs) and license..."
tar -xzf "$TMP_DIR/monaco.tgz" -C "$TMP_DIR" package/min/vs package/LICENSE

# Replace the vendored copy wholesale so removed files don't linger
rm -rf "$VENDOR_DIR"
mkdir -p "$VENDOR_DIR"
cp -R "$TMP_DIR/package/min/vs" "$VENDOR_DIR/vs"
cp "$TMP_DIR/package/LICENSE" "$VENDOR_DIR/LICENSE"
echo "$MONACO_VERSION" > "$VENDOR_DIR/VERSION"

echo "Vendored monaco-editor@${MONACO_VERSION} -> $VENDOR_DIR ($(du -sh "$VENDOR_DIR" | cut -f1))"
echo "Rebuild gonotes to embed the new files."
