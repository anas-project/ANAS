#!/usr/bin/env bash
set -euo pipefail

require_env() {
  local name=$1
  if [ -z "${!name:-}" ]; then
    echo "missing required environment: ${name}" >&2
    exit 64
  fi
}

require_env ANAS_INCUS_IMAGE_ARCHIVE
require_env ANAS_INCUS_IMAGE_OUTPUT
require_env ANAS_INCUS_IMAGE_REVISION
require_env ANAS_DISTROBUILDER
require_env ANAS_DISTROBUILDER_SHA256
require_env ANAS_FORGEJO_RUNNER
require_env ANAS_FORGEJO_RUNNER_SHA256

case "${ANAS_DISTROBUILDER_SHA256}:${ANAS_FORGEJO_RUNNER_SHA256}" in
  *[!0-9a-f:]*|*:|:*|"") echo "image release digests must be lowercase SHA-256" >&2; exit 64 ;;
esac
[ "${#ANAS_DISTROBUILDER_SHA256}" -eq 64 ] || { echo "bad distrobuilder digest length" >&2; exit 64; }
[ "${#ANAS_FORGEJO_RUNNER_SHA256}" -eq 64 ] || { echo "bad forgejo-runner digest length" >&2; exit 64; }

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "${repo_root}"

arch=${ANAS_INCUS_IMAGE_ARCHITECTURE:-$(go env GOARCH)}
case "${arch}" in
  amd64|arm64) ;;
  *) echo "unsupported native architecture: ${arch}" >&2; exit 64 ;;
esac

if [ "${ANAS_INCUS_IMAGE_INIT_ARCHIVE:-}" = "1" ]; then
  go run ./cmd/incus-image-artifacts init --archive "${ANAS_INCUS_IMAGE_ARCHIVE}" >/dev/null
fi

mkdir -p "${ANAS_INCUS_IMAGE_OUTPUT}/recipes" "${ANAS_INCUS_IMAGE_OUTPUT}/images/artifacts"

for iface in incus_container incus_vm; do
  recipe="${ANAS_INCUS_IMAGE_OUTPUT}/recipes/forgejo-runner-${arch}-${iface}.yml"
  export_dir="${ANAS_INCUS_IMAGE_OUTPUT}/images/artifacts/anas/forgejo-runner/${ANAS_INCUS_IMAGE_REVISION}/${arch}/${iface}"

  go run ./cmd/incus-image-artifacts recipe \
    --image forgejo-runner \
    --architecture "${arch}" \
    --interface "${iface}" >"${recipe}"

  go run ./cmd/incus-image-artifacts build \
    --archive "${ANAS_INCUS_IMAGE_ARCHIVE}" \
    --name forgejo-runner \
    --revision "${ANAS_INCUS_IMAGE_REVISION}" \
    --architecture "${arch}" \
    --interface "${iface}" \
    --recipe "${recipe}" \
    --distrobuilder "${ANAS_DISTROBUILDER}" \
    --distrobuilder-sha256 "${ANAS_DISTROBUILDER_SHA256}" \
    --forgejo-runner "${ANAS_FORGEJO_RUNNER}" \
    --forgejo-runner-sha256 "${ANAS_FORGEJO_RUNNER_SHA256}" >/dev/null

  if [ ! -e "${export_dir}" ]; then
    go run ./cmd/incus-image-artifacts export \
      --archive "${ANAS_INCUS_IMAGE_ARCHIVE}" \
      --name forgejo-runner \
      --revision "${ANAS_INCUS_IMAGE_REVISION}" \
      --architecture "${arch}" \
      --interface "${iface}" \
      --output-dir "${export_dir}" >/dev/null
  fi
done

if [ -n "${ANAS_INCUS_IMAGE_PREVIOUS_CATALOG:-}" ]; then
  go run ./cmd/incus-image-artifacts catalog \
    --archive "${ANAS_INCUS_IMAGE_ARCHIVE}" \
    --previous-catalog "${ANAS_INCUS_IMAGE_PREVIOUS_CATALOG}" >"${ANAS_INCUS_IMAGE_OUTPUT}/images/catalog.json"
elif [ "${ANAS_INCUS_IMAGE_FIRST_RELEASE:-}" = "1" ]; then
  go run ./cmd/incus-image-artifacts catalog \
    --archive "${ANAS_INCUS_IMAGE_ARCHIVE}" \
    --first-release >"${ANAS_INCUS_IMAGE_OUTPUT}/images/catalog.json"
else
  echo "set ANAS_INCUS_IMAGE_PREVIOUS_CATALOG or ANAS_INCUS_IMAGE_FIRST_RELEASE=1" >&2
  exit 64
fi

echo "wrote ${ANAS_INCUS_IMAGE_OUTPUT}/images/catalog.json and ${ANAS_INCUS_IMAGE_OUTPUT}/images/artifacts"
