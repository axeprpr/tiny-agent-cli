#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-dev}"
OUTDIR="${2:-dist}"
BIN_NAME="onek"

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
  "windows arm64"
)

find "${OUTDIR}" -type f -delete 2>/dev/null || true
find "${OUTDIR}" -depth -type d -empty -delete 2>/dev/null || true
mkdir -p "${OUTDIR}"

for target in "${targets[@]}"; do
  read -r GOOS GOARCH <<<"${target}"

  ext=""
  if [[ "${GOOS}" == "windows" ]]; then
    ext=".exe"
  fi

  asset_name="${BIN_NAME}-${GOOS}-${GOARCH}${ext}"

  echo "building ${GOOS}/${GOARCH}"
  CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
    go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o "${OUTDIR}/${asset_name}" \
    ./cmd/onek
done

echo "binaries written to ${OUTDIR}"
