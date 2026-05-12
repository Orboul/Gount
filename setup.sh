#!/bin/bash

REQUIRED="1.22.0"
REPO="https://github.com/Orboul/Gount.git"
SOURCE_DIR="$(pwd)/gount"

echo "Thanks for using gount!"
echo "Checking for Go installation..."

if command -v go &>/dev/null; then
    INSTALLED=$(go version | awk '{print $3}' | sed 's/go//')
    echo "Found Go $INSTALLED"

    OLDEST=$(printf '%s\n' "$REQUIRED" "$INSTALLED" | sort -V | head -n1)
    if [[ "$OLDEST" != "$REQUIRED" ]]; then
        echo "Go $INSTALLED is too old — need $REQUIRED or newer."
        echo "Visit https://go.dev/dl/ to download and install Go $REQUIRED or newer."
        exit 1
    fi
    echo "Go version OK."
else
    echo "Go is not installed."
    echo "Visit https://go.dev/dl/ to download and install Go $REQUIRED or newer."
    exit 1
fi

# Download source
echo ""
echo "Creating source directory at $SOURCE_DIR..."
mkdir -p "$SOURCE_DIR"

echo "Downloading gount source..."
git clone --filter=blob:none --sparse "$REPO" "$SOURCE_DIR/repo" &>/dev/null
cd "$SOURCE_DIR/repo"
git sparse-checkout set go &>/dev/null
echo "Download complete."

# Build
echo "Building gount..."
cd go
go build -o "$SOURCE_DIR/gount" ./...

if [[ $? -ne 0 ]]; then
    echo "Build failed."
    exit 1
fi

echo "Build successful! Binary is at $SOURCE_DIR/gount"

# Cleanup prompt
echo ""
read -rp "Remove source code from your system? [y/N] " CLEANUP
if [[ "$CLEANUP" =~ ^[Yy]$ ]]; then
    rm -rf "$SOURCE_DIR/repo"
    echo "Source removed."
else
    echo "Source kept at $SOURCE_DIR/repo"
fi

echo "Done!"
echo
echo "Next steps:"
echo "Run the binary once to generate its config and data folder structure"