#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

README="${ROOT_DIR}/README.md"
HERZOG="${ROOT_DIR}/HERZOG.md"

if [[ ! -f "${README}" ]]; then
  echo "Missing README.md" >&2
  exit 2
fi
if [[ ! -f "${HERZOG}" ]]; then
  echo "Missing HERZOG.md" >&2
  exit 2
fi

extract_headings() {
  # Keep only H2 headings (## ...) which define the stable README structure.
  # Strip trailing whitespace.
  sed -n 's/^##[[:space:]]\+\(.*\)$/\1/p' "$1" | sed 's/[[:space:]]\+$//'
}

readme_h="$(extract_headings "${README}")"
herzog_h="$(extract_headings "${HERZOG}")"

if [[ "${readme_h}" != "${herzog_h}" ]]; then
  echo "HERZOG.md is out of sync with README.md (H2 headings mismatch)." >&2
  echo "--- README.md headings ---" >&2
  echo "${readme_h}" >&2
  echo "--- HERZOG.md headings ---" >&2
  echo "${herzog_h}" >&2
  exit 1
fi

if ! grep -q "This document is AI-generated" "${HERZOG}"; then
  echo "HERZOG.md must include an AI-generated notice." >&2
  exit 1
fi

echo "OK: HERZOG.md headings match README.md"

