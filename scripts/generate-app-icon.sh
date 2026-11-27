#!/bin/sh
# Regenerate the committed icon files from the artwork sources.
#
#   packaging/desktop/icon/app-icon.svg        master artwork (1024-unit grid)
#   packaging/desktop/icon/app-icon-small.svg  simplified, for 32 px and below
#       |
#       v
#   packaging/desktop/visual-tmux-client.svg   Linux scalable icon
#   packaging/desktop/visual-tmux-client.icns  macOS icon, ten representations
#
# Writes into packaging/desktop unless ICON_OUTPUT_DIR names another directory.
# Needs rsvg-convert (Homebrew: librsvg, Debian/Ubuntu: librsvg2-bin) and,
# for the .icns, macOS iconutil. The outputs are committed, so packaging never
# needs to run this; run it after editing the artwork and commit the result.
set -eu
umask 022

project_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
icon_dir="$project_dir/packaging/desktop/icon"
output_dir=${ICON_OUTPUT_DIR:-"$project_dir/packaging/desktop"}
mkdir -p "$output_dir"
source="$icon_dir/app-icon.svg"
small_source="$icon_dir/app-icon-small.svg"

for tool in rsvg-convert; do
  command -v "$tool" >/dev/null 2>&1 || { echo "$tool is required (brew install librsvg)" >&2; exit 1; }
done

# The Linux scalable icon is the master artwork as authored.
cp "$source" "$output_dir/visual-tmux-client.svg"

case "$(uname -s)" in
  Darwin) command -v iconutil >/dev/null 2>&1 || { echo 'iconutil is required' >&2; exit 1; } ;;
  *) echo 'Not macOS: wrote the Linux icon only; no .icns was produced.'; exit 0 ;;
esac

work=$(mktemp -d "${TMPDIR:-/tmp}/vtc-app-icon.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
iconset="$work/visual-tmux-client.iconset"
mkdir -p "$iconset"

# Representation name, pixels, and the artwork it is rendered from.
while read -r name pixels variant; do
  [ -n "$name" ] || continue
  file=$source
  [ "$variant" = small ] && file=$small_source
  rsvg-convert -w "$pixels" -h "$pixels" -f png -o "$iconset/$name.png" "$file"
done <<RENDERS
icon_16x16 16 small
icon_16x16@2x 32 small
icon_32x32 32 small
icon_32x32@2x 64 master
icon_128x128 128 master
icon_128x128@2x 256 master
icon_256x256 256 master
icon_256x256@2x 512 master
icon_512x512 512 master
icon_512x512@2x 1024 master
RENDERS

iconutil -c icns "$iconset" -o "$output_dir/visual-tmux-client.icns"
echo "wrote $output_dir/visual-tmux-client.svg and visual-tmux-client.icns"
