#!/usr/bin/env bash
# Build this checkout and install it for the current user.
#
# The web UI is compiled into the executable with go:embed, so editing anything
# under web/ or internal/ changes nothing you can see until the binary is built
# again and put back where the launcher looks for it. That is the whole reason
# this script exists: it is one command instead of a half-remembered one.
#
#   ./install.sh              install to ~/.local/bin
#   PREFIX=/usr/local/bin ./install.sh
set -euo pipefail
cd "$(dirname "$0")"

prefix="${PREFIX:-$HOME/.local/bin}"
mkdir -p "$prefix"

version="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"

# CGO stays off: the SQLite driver is pure Go, so this needs no toolchain.
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$version" \
    -o "$prefix/dnd-companion" ./cmd/dnd-companion

printf 'installed  %s  (%s)\n' "$prefix/dnd-companion" "$version"

case ":$PATH:" in
    *":$prefix:"*) ;;
    *) printf '\n  %s is not on your PATH.\n' "$prefix" ;;
esac

# Replacing the file does not touch a copy that is already running: it keeps
# executing the image it started with, embedded assets and all.
if pgrep -x dnd-companion >/dev/null 2>&1; then
    printf '\n  A copy is still running the previous build.\n'
    printf '  Close its window and open it again to pick this one up.\n'
fi
