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

rm -rf "${OUTDIR}"
mkdir -p "${OUTDIR}"

for target in "${targets[@]}"; do
  read -r GOOS GOARCH <<<"${target}"

  ext=""
  archive_ext="tar.gz"
  if [[ "${GOOS}" == "windows" ]]; then
    ext=".exe"
    archive_ext="zip"
  fi

  base="${BIN_NAME}_${VERSION}_${GOOS}_${GOARCH}"
  pkg_dir="${OUTDIR}/${base}"
  mkdir -p "${pkg_dir}"

  echo "building ${GOOS}/${GOARCH}"
  CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
    go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o "${pkg_dir}/${BIN_NAME}${ext}" \
    ./cmd/onek

  cp README.md "${pkg_dir}/README.md"

  if [[ "${archive_ext}" == "zip" ]]; then
    (
      cd "${OUTDIR}"
      zip -qr "${base}.zip" "${base}"
    )
  else
    tar -C "${OUTDIR}" -czf "${OUTDIR}/${base}.tar.gz" "${base}"
  fi
done

echo "packages written to ${OUTDIR}"
