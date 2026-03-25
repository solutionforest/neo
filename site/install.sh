#!/bin/sh
set -e

# ──────────────────────────────────────────────
# Vxero Neo Installer
# Usage: curl -fsSL "http://your-nas/neo/download.php?type=install" | sh
# ──────────────────────────────────────────────

BASE_URL="https://get.vxero.dev/neo/download.php"

BINARY="neo"
INSTALL_DIR="/usr/local/bin"

# Detect OS
OS="$(uname -s)"
case "$OS" in
    Darwin)  OS="darwin" ;;
    Linux)   OS="linux" ;;
    MINGW*|MSYS*|CYGWIN*) OS="windows" ;;
    *) echo "Error: unsupported OS: $OS"; exit 1 ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "Error: unsupported architecture: $ARCH"; exit 1 ;;
esac

DOWNLOAD_URL="${BASE_URL}?os=${OS}&arch=${ARCH}"

echo ""
echo "  Vxero Neo Installer"
echo "  ────────────────────"
echo "  OS:   $OS"
echo "  Arch: $ARCH"
echo ""

# Create temp directory
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

# Download
echo "  Downloading neo..."
if command -v curl > /dev/null 2>&1; then
    curl -fsSL -o "${TMPDIR}/${BINARY}" "$DOWNLOAD_URL"
elif command -v wget > /dev/null 2>&1; then
    wget -q -O "${TMPDIR}/${BINARY}" "$DOWNLOAD_URL"
else
    echo "Error: curl or wget required"; exit 1
fi

chmod +x "${TMPDIR}/${BINARY}"

# Install
if [ "$OS" = "windows" ]; then
    INSTALL_DIR="$HOME/bin"
    mkdir -p "$INSTALL_DIR"
    mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}.exe"
    echo "  Installed to ${INSTALL_DIR}/${BINARY}.exe"
    echo "  Add $INSTALL_DIR to your PATH if needed."
elif [ -w "$INSTALL_DIR" ]; then
    mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    echo "  Installed to ${INSTALL_DIR}/${BINARY}"
else
    echo "  Installing to ${INSTALL_DIR} (requires sudo)..."
    sudo mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    echo "  Installed to ${INSTALL_DIR}/${BINARY}"
fi

echo ""
echo "  Done! Run 'neo --help' to get started."
echo ""
