#!/bin/bash
# Makes bx's self-signed code-signing identity, "bx", once. Run it by hand, one time:
#
#   scripts/make-signing-identity.sh [out-dir]    # default ./bx-signing
#
# It writes, into a new directory (mode 0700):
#   bx-signing.p12           the private key and certificate (PKCS#12)
#   bx-signing.p12.password  the .p12's random password
#   bx-signing.cer           the certificate alone (public)
#
# Why: Claude Desktop runs the bx in bx.mcpb as its own app, so on macOS 27
# users give bx itself Full Disk Access. macOS keys that grant to the
# binary's designated requirement. Signed ad hoc, that is the code hash, so
# every update is a new binary and the grant lapses. Signed with this
# certificate, it is the identifier plus this certificate's hash, the same
# for every release, so the grant carries over. Losing the .p12 means a new
# certificate, and every user grants Full Disk Access once more. Keep the
# .p12 and its password in the password manager.
#
# It is self-signed, not a Developer ID: Gatekeeper doesn't trust it, which
# doesn't matter for a binary Claude Desktop unpacks and starts. Nothing goes
# into any keychain; scripts/sign-macos.sh imports the .p12 into a temporary
# keychain for one build.
#
# After running it:
#   base64 -i bx-signing/bx-signing.p12 | gh secret set BX_SIGNING_P12 -R Facets-cloud/bx
#   gh secret set BX_SIGNING_P12_PASSWORD -R Facets-cloud/bx < bx-signing/bx-signing.p12.password
# then move the directory into the password manager and delete it from disk.
set -euo pipefail

name="bx"
out="${1:-bx-signing}"
[ -e "$out" ] && { echo "$out already exists; not replacing an identity (a new one resets every user's grant)" >&2; exit 1; }

# The system LibreSSL, not a Homebrew OpenSSL 3 on PATH: OpenSSL 3 writes a
# PKCS#12 MAC that macOS's `security import` rejects.
openssl=/usr/bin/openssl

mkdir -m 0700 "$out"
umask 077
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

cat > "$tmp/cert.cnf" <<EOF
[req]
distinguished_name = dn
x509_extensions = v3
prompt = no
[dn]
CN = $name
O = Facets
[v3]
basicConstraints = critical, CA:false
keyUsage = critical, digitalSignature
extendedKeyUsage = critical, codeSigning
subjectKeyIdentifier = hash
EOF

# 20 years. The requirement pins this certificate, so replacing it resets grants.
"$openssl" req -x509 -newkey rsa:3072 -nodes -sha256 -days 7300 -config "$tmp/cert.cnf" \
  -keyout "$tmp/key.pem" -out "$tmp/cert.pem" 2>/dev/null
"$openssl" rand -hex 24 > "$out/bx-signing.p12.password"
"$openssl" pkcs12 -export -inkey "$tmp/key.pem" -in "$tmp/cert.pem" -name "$name" \
  -out "$out/bx-signing.p12" -passout "file:$out/bx-signing.p12.password"
"$openssl" x509 -in "$tmp/cert.pem" -outform der -out "$out/bx-signing.cer"

echo "made \"$name\" in $out"
echo "certificate SHA-1 (the designated requirement pins this): $("$openssl" x509 -in "$tmp/cert.pem" -noout -fingerprint -sha1 | cut -d= -f2 | tr -d :)"
