#!/bin/sh
set -eu

GOLANGCI_LINT_VERSION="2.13.2"
BIN_DIR="${1:-$(go env GOPATH)/bin}"

echo "Installing golangci-lint v${GOLANGCI_LINT_VERSION} to ${BIN_DIR}..."
mkdir -p "${BIN_DIR}"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

TARBALL="golangci-lint-${GOLANGCI_LINT_VERSION}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/${TARBALL}"

curl -sSL "$URL" | tar -xz -C "${BIN_DIR}" --strip-components=1 "golangci-lint-${GOLANGCI_LINT_VERSION}-${OS}-${ARCH}/golangci-lint"
echo "Installed $("${BIN_DIR}/golangci-lint" version)"
