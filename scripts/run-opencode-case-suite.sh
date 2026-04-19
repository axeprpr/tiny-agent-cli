#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CASE_FILE="${ROOT_DIR}/docs/opencode-test-cases-001-200.md"

if [[ ! -f "${CASE_FILE}" ]]; then
  echo "case file not found: ${CASE_FILE}" >&2
  exit 1
fi

# Verify 200 sequential case IDs.
case_lines="$(rg -n '^\| [0-9]{3} \|' "${CASE_FILE}" || true)"
case_count="$(printf '%s\n' "${case_lines}" | sed '/^$/d' | wc -l | tr -d ' ')"
if [[ "${case_count}" -ne 200 ]]; then
  echo "expected 200 case rows, got ${case_count}" >&2
  exit 1
fi

expected=1
while IFS= read -r line; do
  [[ -z "${line}" ]] && continue
  id="$(printf '%s' "${line}" | sed -E 's/^.*\| ([0-9]{3}) \|.*$/\1/')"
  num=$((10#${id}))
  if [[ "${num}" -ne "${expected}" ]]; then
    echo "case id sequence broken at expected $(printf '%03d' "${expected}"), got ${id}" >&2
    exit 1
  fi
  expected=$((expected+1))
done <<< "$(printf '%s\n' "${case_lines}" | cut -d: -f2-)"

echo "[opencode-case-suite] case catalog check passed: 200/200"

# Verify each row has detailed fields and a linked test reference.
if rg -n '对应的命令/操作|执行与“' "${CASE_FILE}" >/dev/null 2>&1; then
  echo "case catalog still contains placeholder execution text" >&2
  exit 1
fi

if ! rg -n '^\| [0-9]{3} \| .* \| `.+_test\.go:Test.+' "${CASE_FILE}" >/dev/null 2>&1; then
  echo "case catalog missing linked test references in expected format" >&2
  exit 1
fi

echo "[opencode-case-suite] detail checks passed"

cd "${ROOT_DIR}"
HOME="${HOME:-/root}" \
GOPATH="${GOPATH:-/root/go}" \
XDG_CACHE_HOME="${XDG_CACHE_HOME:-${HOME:-/root}/.cache}" \
GOCACHE="${GOCACHE:-${XDG_CACHE_HOME:-/root/.cache}/go-build}" \
  go test ./... -count=1

echo "[opencode-case-suite] go test passed"
