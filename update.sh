#!/usr/bin/env bash
# Install the newest published release for the current user.
#
# This is the other half of install.sh. That one builds whatever is in this
# checkout, which is what you want while you are working on it; this one fetches
# the last thing that was tagged, cross-compiled and started successfully on
# Linux, Windows and macOS by CI. Use it on a machine that has no Go, or to get
# back to a known-good build after an experiment.
#
#   ./update.sh                    install the latest release to ~/.local/bin
#   PREFIX=/usr/local/bin ./update.sh
#   FORCE=1 ./update.sh            reinstall even if that version is already here
#
# Untagged builds are not here — they are attached to every CI run instead, on
# the Actions page under the run you want.
set -euo pipefail
cd "$(dirname "$0")"

repo="MasterProgramerCPU/dnd-companion"
prefix="${PREFIX:-$HOME/.local/bin}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "update.sh needs $1" >&2; exit 1; }; }
need curl

# Which of the six binaries is this machine's.
case "$(uname -s)" in
    Linux)  goos=linux  ;;
    Darwin) goos=darwin ;;
    *) echo "No release binary for $(uname -s). On Windows, download the .exe from the releases page." >&2; exit 1 ;;
esac
case "$(uname -m)" in
    x86_64|amd64)  goarch=amd64 ;;
    aarch64|arm64) goarch=arm64 ;;
    *) echo "No release binary for $(uname -m)." >&2; exit 1 ;;
esac
asset="dnd-companion-$goos-$goarch"

# sha256sum is GNU; macOS ships shasum instead.
if command -v sha256sum >/dev/null 2>&1; then
    checksum() { sha256sum -c -; }
else
    need shasum
    checksum() { shasum -a 256 -c -; }
fi

printf 'looking up the latest release of %s\n' "$repo"
tag="$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
[ -n "$tag" ] || { echo "could not work out the latest release" >&2; exit 1; }

# Nothing to do if that is already the one installed. -version prints
# "dnd-companion v0.1.1", which is the tag with the name in front of it.
installed=""
if [ -x "$prefix/dnd-companion" ]; then
    installed="$("$prefix/dnd-companion" -version 2>/dev/null | awk '{print $2}')" || true
fi
if [ "$installed" = "$tag" ] && [ -z "${FORCE:-}" ]; then
    printf 'already on %s — nothing to do (FORCE=1 to reinstall)\n' "$tag"
    exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

base="https://github.com/$repo/releases/download/$tag"
printf 'downloading   %s  %s\n' "$tag" "$asset"
curl -fsSL -o "$tmp/$asset"    "$base/$asset"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"

# Verify before it goes anywhere near the PATH. Only this machine's line is
# checked, because the other five files were never downloaded.
printf 'verifying     '
( cd "$tmp" && grep " $asset\$" SHA256SUMS | checksum ) >/dev/null \
    || { echo "checksum did NOT match — refusing to install" >&2; exit 1; }
printf 'sha256 ok\n'

mkdir -p "$prefix"
chmod +x "$tmp/$asset"
mv -f "$tmp/$asset" "$prefix/dnd-companion"

printf 'installed     %s  (%s' "$prefix/dnd-companion" "$tag"
[ -n "$installed" ] && printf ', was %s' "$installed"
printf ')\n'

case ":$PATH:" in
    *":$prefix:"*) ;;
    *) printf '\n  %s is not on your PATH.\n' "$prefix" ;;
esac

# Same caveat as install.sh: replacing the file does not touch a copy that is
# already running, which keeps serving the version it started with.
printf '\n  A copy that is already open keeps running the old build.\n'
printf '  Close its window and open it again to pick this one up.\n'
