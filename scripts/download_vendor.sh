#!/bin/bash

# Download vendor libraries for GoNotes Web

VENDOR_DIR="server/static/vendor"

echo "Downloading vendor libraries..."

# Create vendor directory if it doesn't exist
mkdir -p "$VENDOR_DIR"

# Download MessagePack
echo "Downloading MessagePack..."
curl -L "https://unpkg.com/@msgpack/msgpack@latest/dist/msgpack.min.js" -o "$VENDOR_DIR/msgpack.min.js"

# Monaco Editor requires manual download due to its complexity
echo ""
echo "Note: Monaco Editor needs to be downloaded separately."
echo "Visit: https://microsoft.github.io/monaco-editor/"
echo "Or use the CDN version by updating the layout.go file."

echo ""
echo "Vendor libraries downloaded successfully!"
echo "Don't forget to rebuild the application to embed the new files."