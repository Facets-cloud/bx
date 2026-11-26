#!/bin/sh
# Installs bx, the browser exec CLI, on macOS:
#
#   curl -fsSL https://facets-cloud.github.io/bx/install.sh | sh
#
# It downloads the latest release for this Mac's CPU from GitHub, checks it
# against the release's checksums.txt, and puts bx in ~/.local/bin.
#   BX_VERSION=v0.9.0   install that release instead of the latest
#   BX_INSTALL_DIR=dir  install into dir instead of ~/.local/bin
#   BX_RELEASES_URL     where releases live (tests point it at a local server)
# The bx-cli plugin carries a copy of this file (plugin-cli/hooks/install.sh).
set -eu

if [ "$(uname -s)" != Darwin ]; then
  echo "bx: only macOS is supported for now" >&2
  exit 1
fi
case $(uname -m) in
  arm64) arch=arm64 ;;
  x86_64) arch=amd64 ;;
  *) echo "bx: no release for CPU $(uname -m)" >&2; exit 1 ;;
esac

releases=${BX_RELEASES_URL:-https://github.com/Facets-cloud/bx/releases}
if [ -n "${BX_VERSION:-}" ]; then
  base=$releases/download/$BX_VERSION
else
  base=$releases/latest/download
fi
dir=${BX_INSTALL_DIR:-$HOME/.local/bin}
asset=bx_darwin_$arch.tar.gz

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
if ! (cd "$tmp" && grep " $asset\$" checksums.txt | shasum -a 256 -c - >/dev/null 2>&1); then
  echo "bx: $asset does not match the release's checksums.txt; not installing" >&2
  exit 1
fi
tar -xzf "$tmp/$asset" -C "$tmp" bx

mkdir -p "$dir"
# Copy next to the target, then rename, so a running bx is never half-written.
cp "$tmp/bx" "$dir/.bx.$$"
chmod 755 "$dir/.bx.$$"
xattr -d com.apple.quarantine "$dir/.bx.$$" 2>/dev/null || true
mv -f "$dir/.bx.$$" "$dir/bx"

echo "installed $("$dir/bx" version 2>/dev/null || echo bx) to $dir/bx"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "add $dir to your PATH to run bx from a terminal" ;;
esac
