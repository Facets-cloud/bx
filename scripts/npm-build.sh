#!/bin/bash
# Build the npm packages for one version: npm-build.sh v0.2.0
# Fills npm/bx-darwin-{arm64,x64}/bin/bx and sets every package.json
# version. Publish the platform packages before npm/bx.
set -euo pipefail
tag="${1:?usage: npm-build.sh vX.Y.Z}"
version="${tag#v}"
cd "$(dirname "$0")/.."
for pair in arm64:arm64 x64:amd64; do
  npmarch="${pair%%:*}"; goarch="${pair##*:}"
  dir="npm/bx-darwin-$npmarch"
  GOOS=darwin GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=$tag" -o "$dir/bin/bx" .
  cp LICENSE THIRD_PARTY_NOTICES "$dir/"
done
cp LICENSE THIRD_PARTY_NOTICES npm/bx/
for f in npm/*/package.json; do
  node -e '
    const fs = require("fs"), [f, v] = process.argv.slice(1);
    const p = JSON.parse(fs.readFileSync(f));
    p.version = v;
    for (const d of Object.keys(p.optionalDependencies || {})) p.optionalDependencies[d] = v;
    fs.writeFileSync(f, JSON.stringify(p, null, 2) + "\n");
  ' "$f" "$version"
done
echo "built npm packages for $version"
