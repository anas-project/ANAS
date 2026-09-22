#!/usr/bin/env bash
set -euo pipefail
umask 077

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

# History and the fresh output destination are explicit admission conditions,
# not errors discovered after a privileged bake or a truncated catalog write.
case "${ANAS_INCUS_IMAGE_FIRST_RELEASE:-}" in
  ""|0|1) ;;
  *) echo "ANAS_INCUS_IMAGE_FIRST_RELEASE must be 0 or 1" >&2; exit 64 ;;
esac
case "${ANAS_INCUS_IMAGE_INIT_ARCHIVE:-}" in
  ""|0|1) ;;
  *) echo "ANAS_INCUS_IMAGE_INIT_ARCHIVE must be 0 or 1" >&2; exit 64 ;;
esac
history_args=()
if [ -n "${ANAS_INCUS_IMAGE_PREVIOUS_CATALOG:-}" ]; then
  [ "${ANAS_INCUS_IMAGE_FIRST_RELEASE:-}" != 1 ] || {
    echo "previous catalog and first release are mutually exclusive" >&2; exit 64;
  }
  history_args=(--previous-catalog "${ANAS_INCUS_IMAGE_PREVIOUS_CATALOG}")
elif [ "${ANAS_INCUS_IMAGE_FIRST_RELEASE:-}" = 1 ]; then
  history_args=(--first-release)
else
  echo "set ANAS_INCUS_IMAGE_PREVIOUS_CATALOG or ANAS_INCUS_IMAGE_FIRST_RELEASE=1" >&2
  exit 64
fi
if [ -e "${ANAS_INCUS_IMAGE_OUTPUT}" ] || [ -L "${ANAS_INCUS_IMAGE_OUTPUT}" ]; then
  echo "image release output must be a new private directory" >&2
  exit 64
fi

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

# Validate existing history before invoking any builder. The final bundle
# repeats this check under the same archive gate used for exporting its bytes.
go run ./cmd/incus-image-artifacts catalog \
  --archive "${ANAS_INCUS_IMAGE_ARCHIVE}" "${history_args[@]}" >/dev/null

mkdir "${ANAS_INCUS_IMAGE_OUTPUT}"
mkdir "${ANAS_INCUS_IMAGE_OUTPUT}/recipes"

for iface in incus_container incus_vm; do
  recipe="${ANAS_INCUS_IMAGE_OUTPUT}/recipes/forgejo-runner-${arch}-${iface}.yml"

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
done

# Restore every committed target/revision, not just this build's two targets.
# The tool itself writes catalog.json last; shell redirection cannot truncate
# an existing catalog or publish one without the matching historical bytes.
go run ./cmd/incus-image-artifacts bundle \
  --archive "${ANAS_INCUS_IMAGE_ARCHIVE}" \
  "${history_args[@]}" \
  --output-dir "${ANAS_INCUS_IMAGE_OUTPUT}/images" >/dev/null

echo "wrote ${ANAS_INCUS_IMAGE_OUTPUT}/images/catalog.json and ${ANAS_INCUS_IMAGE_OUTPUT}/images/artifacts"
