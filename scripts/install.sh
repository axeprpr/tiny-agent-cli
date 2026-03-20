#!/usr/bin/env bash
set -euo pipefail

REPO="axeprpr/tiny-agent-cli"
BIN_NAME="tacli"
VERSION="${TACLI_VERSION:-${ONEK_VERSION:-latest}}"
INSTALL_DIR="${TACLI_INSTALL_DIR:-${ONEK_INSTALL_DIR:-$HOME/.local/bin}}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "${OS}" in
  linux) os="linux" ;;
  darwin) os="darwin" ;;
  *)
    echo "unsupported OS: ${OS}" >&2
    exit 1
    ;;
esac

case "${ARCH}" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *)
    echo "unsupported architecture: ${ARCH}" >&2
    exit 1
    ;;
esac

asset="${BIN_NAME}-${os}-${arch}"
base="https://github.com/${REPO}/releases"

if [[ "${VERSION}" == "latest" ]]; then
  url="${base}/latest/download/${asset}"
else
  url="${base}/download/${VERSION}/${asset}"
fi

mkdir -p "${INSTALL_DIR}"
target="${INSTALL_DIR}/${BIN_NAME}"

tmp="$(mktemp)"
trap 'rm -f "${tmp}"' EXIT

echo "downloading ${url}"
curl -fsSL "${url}" -o "${tmp}"
chmod +x "${tmp}"
mv "${tmp}" "${target}"

echo "installed ${BIN_NAME} to ${target}"
echo "run: ${target} version"
