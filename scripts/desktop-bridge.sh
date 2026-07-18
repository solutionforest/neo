#!/usr/bin/env sh
# Build the neo-bridge sidecar for a Rust target triple and place it under the
# filename Tauri's externalBin expects:
#   apps/desktop/src-tauri/binaries/neo-bridge-<triple>[.exe]
#
# Usage:
#   scripts/desktop-bridge.sh [target-triple]
#
# With no argument the host triple is detected (via `rustc -vV` when available,
# falling back to uname). Version stamping:
#   VERSION  — desktop/bridge semantic version (default: dev)
#   COMMIT   — git commit built from       (default: git rev-parse, else unknown)
#
# This intentionally uses the host Go toolchain, matching the desktop build path
# (apps/desktop/src-tauri/build.rs). It never touches the Dockerized CLI build
# (`make build`) and never adds anything to the root Go module.
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
out_dir="$repo_root/apps/desktop/src-tauri/binaries"

triple="${1:-}"
if [ -z "$triple" ]; then
    if command -v rustc >/dev/null 2>&1; then
        triple=$(rustc -vV | sed -n 's/^host: //p')
    else
        os=$(uname -s)
        arch=$(uname -m)
        case "$arch" in
            arm64 | aarch64) arch=aarch64 ;;
            x86_64 | amd64) arch=x86_64 ;;
            *) echo "desktop-bridge.sh: unsupported host arch: $arch" >&2 && exit 1 ;;
        esac
        case "$os" in
            Darwin) triple="$arch-apple-darwin" ;;
            Linux) triple="$arch-unknown-linux-gnu" ;;
            MINGW* | MSYS* | CYGWIN*) triple="$arch-pc-windows-msvc" ;;
            *) echo "desktop-bridge.sh: unsupported host OS: $os" >&2 && exit 1 ;;
        esac
    fi
fi

case "$triple" in
    *windows*) goos=windows ;;
    *apple* | *darwin*) goos=darwin ;;
    *linux*) goos=linux ;;
    *) echo "desktop-bridge.sh: cannot map triple to GOOS: $triple" >&2 && exit 1 ;;
esac
case "$triple" in
    x86_64-*) goarch=amd64 ;;
    aarch64-* | arm64-*) goarch=arm64 ;;
    i686-* | i586-*) goarch=386 ;;
    *) echo "desktop-bridge.sh: cannot map triple to GOARCH: $triple" >&2 && exit 1 ;;
esac

version="${VERSION:-dev}"
commit="${COMMIT:-$(git -C "$repo_root" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)}"

out="$out_dir/neo-bridge-$triple"
[ "$goos" = windows ] && out="$out.exe"

mkdir -p "$out_dir"
echo "building neo-bridge $version ($commit) for $triple -> $out"
cd "$repo_root"
CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" "${GO:-go}" build -trimpath \
    -ldflags "-s -w -X main.bridgeVersion=$version -X main.coreVersion=$version -X main.buildCommit=$commit" \
    -o "$out" ./cmd/neo-bridge
