#!/bin/sh
# Package a native binary whose embedded frontend has already been built.
set -eu

project_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
version=${DESKTOP_VERSION:-0.5.0-desktop.dev}
output_dir=${OUTPUT_DIR:-"$project_dir/desktop-packages"}
binary=${DESKTOP_BINARY:?Set DESKTOP_BINARY to a natively built visual-tmux-desktop}
case "$version" in
  [0-9]*) ;;
  *) echo 'DESKTOP_VERSION must start with a digit' >&2; exit 1 ;;
esac
case "$version" in
  *[!0-9A-Za-z.+~-]*) echo 'invalid DESKTOP_VERSION' >&2; exit 1 ;;
esac
test -x "$binary"
mkdir -p "$output_dir"
output_dir=$(CDPATH= cd -- "$output_dir" && pwd)
staging=$(mktemp -d "${TMPDIR:-/tmp}/vtc-desktop-package.XXXXXX")
trap 'rm -rf "$staging"' EXIT HUP INT TERM

case "$(uname -s)/$(uname -m)" in
  Linux/x86_64)
    root="$staging/root"
    mkdir -p "$root/DEBIAN" "$root/usr/bin" "$root/usr/share/applications" \
      "$root/usr/share/icons/hicolor/scalable/apps" "$root/usr/share/doc/visual-tmux-desktop"
    cp "$binary" "$root/usr/bin/visual-tmux-desktop"
    chmod 755 "$root/usr/bin/visual-tmux-desktop"
    cp "$project_dir/packaging/desktop/visual-tmux-client.desktop" \
      "$root/usr/share/applications/io.github.ak12official.visualtmuxclient.desktop"
    cp "$project_dir/packaging/desktop/visual-tmux-client.svg" "$root/usr/share/icons/hicolor/scalable/apps/"
    cp "$project_dir/LICENSE" "$root/usr/share/doc/visual-tmux-desktop/copyright"
    cp "$project_dir/docs/desktop-verification.md" "$root/usr/share/doc/visual-tmux-desktop/VERIFICATION.md"
    cat > "$root/DEBIAN/control" <<CONTROL
Package: visual-tmux-desktop
Version: $version
Architecture: amd64
Maintainer: AK12-Official <noreply@github.com>
Section: utils
Priority: optional
Depends: libgtk-4-1 (>= 4.14), libwebkitgtk-6.0-4 (>= 2.44), libc6 (>= 2.39)
Recommends: tmux
Description: Visual tmux desktop workspace (development build)
 Embeds the Vue interface and Go backend. No Go or Node runtime is needed.
 Runtime acceptance remains subject to the included verification matrix.
CONTROL
    dpkg-deb --build --root-owner-group "$root" "$output_dir/visual-tmux-desktop_${version}_amd64.deb"
    ;;
  Darwin/arm64|Darwin/x86_64)
    arch=$(uname -m)
    app="$staging/Visual Tmux Client.app"
    mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
    cp "$binary" "$app/Contents/MacOS/visual-tmux-desktop"
    chmod 755 "$app/Contents/MacOS/visual-tmux-desktop"
    cp "$project_dir/docs/desktop-verification.md" "$app/Contents/Resources/VERIFICATION.md"
    cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>visual-tmux-desktop</string>
<key>CFBundleIdentifier</key><string>io.github.ak12official.visualtmuxclient</string>
<key>CFBundleName</key><string>Visual Tmux Client</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>0.5.0</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSMinimumSystemVersion</key><string>12.0</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
    plutil -lint "$app/Contents/Info.plist"
    codesign --force --sign - "$app/Contents/MacOS/visual-tmux-desktop"
    codesign --force --sign - "$app"
    codesign --verify --strict "$app"
    printf '%s\n' 'Development build: ad-hoc signed, no Developer ID or notarization.' > "$app/Contents/Resources/SIGNING.txt"
    # Include the label before signing again; the resource seal covers it.
    codesign --force --sign - "$app"
    codesign --verify --strict "$app"
    ditto -c -k --keepParent "$app" "$output_dir/visual-tmux-desktop_${version}_${arch}_development-unsigned.zip"
    ;;
  *) echo 'Build packages natively on Ubuntu 24.04 amd64 or macOS arm64/amd64' >&2; exit 1 ;;
esac
