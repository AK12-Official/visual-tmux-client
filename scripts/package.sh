#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
version=${VERSION:-0.1.0}
output_dir=${OUTPUT_DIR:-"$project_dir/release"}
targets=${TARGETS:-"$(go env GOOS)/$(go env GOARCH)"}

case "$version" in
  *[!0-9A-Za-z._-]*|'')
    echo "invalid VERSION: $version" >&2
    exit 1
    ;;
esac

npm --prefix "$project_dir/hub/web" ci
npm --prefix "$project_dir/hub/web" run build
npm --prefix "$project_dir/hub/web" test

mkdir -p "$output_dir"
find "$output_dir" -maxdepth 1 -type f \( -name 'visual-tmux-client_*.tar.gz' -o -name 'checksums.txt' \) -delete

for target in $targets; do
  goos=${target%/*}
  goarch=${target#*/}
  if [ "$goos" = "$target" ] || [ -z "$goos" ] || [ -z "$goarch" ]; then
    echo "invalid target: $target (expected GOOS/GOARCH)" >&2
    exit 1
  fi
  case "$goos/$goarch" in
    darwin/amd64|darwin/arm64|linux/amd64|linux/arm64) ;;
    *)
      echo "unsupported target: $target" >&2
      exit 1
      ;;
  esac

  archive_name="visual-tmux-client_${version}_${goos}_${goarch}"
  staging_dir=$(mktemp -d "${TMPDIR:-/tmp}/visual-tmux-client-package.XXXXXX")
  package_dir="$staging_dir/$archive_name"
  mkdir -p "$package_dir"

  (
    cd "$project_dir/hub"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
      -trimpath \
      -ldflags "-s -w -X main.version=$version" \
      -o "$package_dir/visual-tmux-client" ./cmd/visual-tmux-client
  )
  cp "$project_dir/README.md" "$project_dir/README.zh-CN.md" "$project_dir/LICENSE" "$package_dir/"
  mkdir -p "$package_dir/configs"
  cp "$project_dir/configs/visual-tmux-client.example.yaml" "$package_dir/configs/"
  tar -C "$staging_dir" -czf "$output_dir/$archive_name.tar.gz" "$archive_name"
  rm -rf "$staging_dir"
done

(
  cd "$output_dir"
  shasum -a 256 visual-tmux-client_*.tar.gz > checksums.txt
)

echo "release artifacts written to $output_dir"
