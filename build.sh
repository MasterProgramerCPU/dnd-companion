#!/usr/bin/env bash
# Build the app for every platform, from whichever one you happen to be on.
#
# There is no toolchain to install and no CI to wait for: the SQLite driver is
# pure Go, so CGO stays off and Go's own cross-compiler does the rest.
set -euo pipefail
cd "$(dirname "$0")"

out="${1:-dist}"
mkdir -p "$out"
rm -f "$out"/dnd-companion-*

version="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"

for target in windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/arm64 darwin/amd64; do
    os="${target%%/*}"
    arch="${target##*/}"
    ext=""
    [ "$os" = windows ] && ext=".exe"

    printf '  %-16s' "$os/$arch"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
        -trimpath -ldflags="-s -w -X main.version=$version" \
        -o "$out/dnd-companion-$os-$arch$ext" ./cmd/dnd-companion
    printf 'ok\n'
done

echo
ls -lh "$out" | tail -n +2 | awk '{printf "  %-38s %s\n", $9, $5}'
