#!/bin/bash

# Download vendor libraries for GoNotes Web

VENDOR_DIR="server/static/vendor"

echo "Downloading vendor libraries..."

# Create vendor directory if it doesn't exist
mkdir -p "$VENDOR_DIR"

# Download Alpine.js
echo "Downloading Alpine.js..."
curl -L "https://unpkg.com/alpinejs@3.x.x/dist/cdn.min.js" -o "$VENDOR_DIR/alpine.min.js"

# Download HTMX
echo "Downloading HTMX..."
curl -L "https://unpkg.com/htmx.org@latest/dist/htmx.min.js" -o "$VENDOR_DIR/htmx.min.js"

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