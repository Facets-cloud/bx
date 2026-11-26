#!/bin/bash
# Build bx.mcpb, the Claude Desktop extension, for one version:
#   mcpb-build.sh v0.2.0 [out-dir]     (default out-dir: dist)
# A .mcpb is a zip with manifest.json at its root (spec:
# github.com/modelcontextprotocol/mcpb, MANIFEST.md). It carries one
# universal macOS binary and runs `bx mcp`, so Claude Desktop users need
# neither Node nor a terminal. BX_MCPB=1 tells bx it lives in the extension,
# where it must not replace itself (`bx update` points at a new bx.mcpb).
set -euo pipefail
tag="${1:?usage: mcpb-build.sh vX.Y.Z [out-dir]}"
version="${tag#v}"
cd "$(dirname "$0")/.."
out="$(mkdir -p "${2:-dist}" && cd "${2:-dist}" && pwd)"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

mkdir "$stage/server"
for arch in arm64 amd64; do
  GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=$tag" -o "$stage/bx_$arch" .
done
lipo -create -output "$stage/server/bx" "$stage/bx_arm64" "$stage/bx_amd64"
rm "$stage/bx_arm64" "$stage/bx_amd64"
chmod 755 "$stage/server/bx"
# With BX_SIGNING_P12 set, the stable "bx" identity, so a Full Disk Access
# grant to bx survives updates; otherwise ad hoc (scripts/sign-macos.sh).
scripts/sign-macos.sh "$stage/server/bx"
cp LICENSE THIRD_PARTY_NOTICES "$stage/"
cp scripts/mcpb-icon.png "$stage/icon.png"

cat > "$stage/manifest.json" <<EOF
{
  "manifest_version": "0.3",
  "name": "bx",
  "display_name": "bx",
  "version": "$version",
  "description": "Drive the Chromium browser you already have open, in a profile you name, with your logins.",
  "long_description": "bx lets Claude click through pages and read their console, network and storage in your own running browser (Brave, Chrome, Edge, Arc or Vivaldi on Chromium 144 or later). Turn on remote debugging once at the browser's inspect page, on macOS 27 and later give bx itself Full Disk Access (bx names the file to add), not Claude, and click Allow once per browser run.",
  "author": { "name": "Facets", "url": "https://facets-cloud.github.io/bx/" },
  "homepage": "https://facets-cloud.github.io/bx/",
  "support": "https://github.com/Facets-cloud/bx/issues",
  "repository": { "type": "git", "url": "https://github.com/Facets-cloud/bx" },
  "license": "MIT",
  "icon": "icon.png",
  "keywords": ["browser", "chromium", "automation", "devtools"],
  "server": {
    "type": "binary",
    "entry_point": "server/bx",
    "mcp_config": {
      "command": "\${__dirname}/server/bx",
      "args": ["mcp"],
      "env": { "BX_MCPB": "1" }
    }
  },
  "compatibility": { "platforms": ["darwin"] }
}
EOF

rm -f "$out/bx.mcpb"
# zip keeps each file's Unix mode, so the binary stays executable once Claude
# Desktop unpacks it. -D leaves out directory entries, which mcpb's unpacker
# tries to write as files; -X leaves out other extra fields.
(cd "$stage" && zip -qrDX "$out/bx.mcpb" .)
echo "$out/bx.mcpb"
