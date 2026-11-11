#!/bin/sh
# Optional, explicit signing stage. Never invoked for pull-request builds.
set -eu
test "$(uname -s)" = Darwin
: "${MACOS_CERTIFICATE_P12_BASE64:?Missing signing certificate}"
: "${MACOS_CERTIFICATE_PASSWORD:?Missing certificate password}"
: "${MACOS_SIGNING_IDENTITY:?Missing Developer ID identity}"
: "${MACOS_NOTARY_API_KEY_P8_BASE64:?Missing notary API key}"
: "${MACOS_NOTARY_KEY_ID:?Missing notary key ID}"
: "${MACOS_NOTARY_ISSUER_ID:?Missing notary issuer ID}"
case "$MACOS_SIGNING_IDENTITY" in
  'Developer ID Application: '*) ;;
  *) echo 'A Developer ID Application identity is required' >&2; exit 1 ;;
esac
input=${1:?Pass one development .app ZIP}
output_dir=${OUTPUT_DIR:-desktop-notarized}
mkdir -p "$output_dir"
output_dir=$(CDPATH= cd -- "$output_dir" && pwd)
umask 077
staging=$(mktemp -d "${TMPDIR:-/tmp}/vtc-notary.XXXXXX")
keychain="$staging/signing.keychain-db"
cleanup() {
  security delete-keychain "$keychain" >/dev/null 2>&1 || true
  rm -rf "$staging"
}
trap cleanup EXIT HUP INT TERM
printf '%s' "$MACOS_CERTIFICATE_P12_BASE64" | base64 -D > "$staging/certificate.p12"
printf '%s' "$MACOS_NOTARY_API_KEY_P8_BASE64" | base64 -D > "$staging/notary.p8"
keychain_password=$(uuidgen)
security create-keychain -p "$keychain_password" "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
security import "$staging/certificate.p12" -k "$keychain" \
  -P "$MACOS_CERTIFICATE_PASSWORD" -T /usr/bin/codesign >/dev/null
security set-key-partition-list -S apple-tool:,apple: -s -k "$keychain_password" "$keychain" >/dev/null
ditto -x -k "$input" "$staging/unpacked"
app="$staging/unpacked/Visual Tmux Client.app"
test -x "$app/Contents/MacOS/visual-tmux-desktop"
printf '%s\n' 'Developer ID signed and notarized; assess the included verification matrix separately.' \
  > "$app/Contents/Resources/SIGNING.txt"
codesign --force --options runtime --timestamp --keychain "$keychain" \
  --sign "$MACOS_SIGNING_IDENTITY" "$app/Contents/MacOS/visual-tmux-desktop"
codesign --force --options runtime --timestamp --keychain "$keychain" \
  --sign "$MACOS_SIGNING_IDENTITY" "$app"
codesign --verify --deep --strict "$app"
ditto -c -k --keepParent "$app" "$staging/submission.zip"
xcrun notarytool submit "$staging/submission.zip" --wait --timeout 20m \
  --key "$staging/notary.p8" --key-id "$MACOS_NOTARY_KEY_ID" --issuer "$MACOS_NOTARY_ISSUER_ID" \
  --output-format json > "$staging/notary-result.json"
test "$(plutil -extract status raw "$staging/notary-result.json")" = Accepted
xcrun stapler staple "$app"
xcrun stapler validate "$app"
codesign --verify --deep --strict "$app"
spctl --assess --type execute --verbose=2 "$app"
name=$(basename "$input" _development-unsigned.zip)
test "$name" != "$(basename "$input")"
ditto -c -k --keepParent "$app" "$staging/verified.zip"
mv "$staging/verified.zip" "$output_dir/${name}_developer-id-notarized.zip"
