#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"
case "$mode" in
  validate)
    if [[ "$#" != 3 ]]; then
      echo "usage: $0 validate <source> <platforms>" >&2
      exit 2
    fi
    source_image="$2"
    target_image=""
    platforms="$3"
    ;;
  copy)
    if [[ "$#" != 4 ]]; then
      echo "usage: $0 copy <source> <target> <platforms>" >&2
      exit 2
    fi
    source_image="$2"
    target_image="$3"
    platforms="$4"
    ;;
  *)
    echo "usage: $0 validate <source> <platforms> | copy <source> <target> <platforms>" >&2
    exit 2
    ;;
esac

IFS=',' read -r -a requested_platforms <<<"$platforms"
if [[ "${#requested_platforms[@]}" == 0 ]]; then
  echo "at least one runtime platform is required" >&2
  exit 1
fi
for platform in "${requested_platforms[@]}"; do
  if [[ ! "$platform" =~ ^linux/(amd64|arm64)$ ]]; then
    echo "unsupported runtime platform $platform; ANAS publishes linux/amd64 and linux/arm64" >&2
    exit 1
  fi
done

raw_manifest="$(docker buildx imagetools inspect --raw "$source_image")"
source_repository="${source_image%%@*}"
last_component="${source_repository##*/}"
if [[ "$last_component" == *:* ]]; then
  source_repository="${source_repository%:*}"
fi

sources=()
source_digests=()
if jq -e '.manifests | type == "array"' <<<"$raw_manifest" >/dev/null 2>&1; then
  for platform in "${requested_platforms[@]}"; do
    os="${platform%/*}"
    architecture="${platform#*/}"
    digest="$(jq -r --arg os "$os" --arg architecture "$architecture" '
      first(
        .manifests[]
        | select(.platform.os == $os and .platform.architecture == $architecture)
        | .digest
      ) // empty
    ' <<<"$raw_manifest")"
    if [[ -z "$digest" ]]; then
      echo "$source_image does not publish $platform" >&2
      exit 1
    fi
    sources+=("${source_repository}@${digest}")
    source_digests+=("${digest}")
  done
else
  if [[ "${#requested_platforms[@]}" != 1 ]]; then
    echo "$source_image is a single-platform manifest but $platforms was requested" >&2
    exit 1
  fi
  sources+=("$source_image")
fi

echo "$source_image provides $platforms"
if [[ "$mode" == "validate" ]]; then
  exit 0
fi

if ! command -v crane >/dev/null 2>&1; then
  echo "crane is required for registry-to-registry copies; run scripts/ci/install-crane.sh first" >&2
  exit 1
fi

if [[ "${#source_digests[@]}" == 0 ]]; then
  source_digests+=("$(crane digest "$source_image")")
fi

# Compose a runtime-only index. Provenance and SBOM descriptors use
# os/architecture=unknown and can exceed CNB's per-manifest metadata limit;
# they remain available on ANAS-built GHCR packages but are not deployment
# inputs and are deliberately omitted from mirrors.
#
# A new CNB package can briefly return 404 after the registry accepts the
# login but before its package metadata is visible to every backend. Large
# BuildKit cross-registry copies can also fail at the CNB edge with an HTTP/2
# PROTOCOL_ERROR. Crane performs the copy in this process, HTTP/2 is disabled
# for registry traffic, and each platform is retried independently. Completed
# blobs are discovered at the target and are not uploaded again.
copy_attempts="${RUNTIME_IMAGE_COPY_ATTEMPTS:-5}"
copy_retry_delay="${RUNTIME_IMAGE_COPY_RETRY_DELAY_SECONDS:-5}"
crane_go_debug="${RUNTIME_IMAGE_CRANE_GODEBUG:-http2client=0}"
if [[ ! "$copy_attempts" =~ ^[1-9][0-9]*$ || ! "$copy_retry_delay" =~ ^[0-9]+$ ]]; then
  echo "copy retry settings must be non-negative integers and attempts must be positive" >&2
  exit 1
fi

retry() {
  local description="$1"
  shift
  local attempt
  for ((attempt = 1; attempt <= copy_attempts; attempt++)); do
    if env GODEBUG="$crane_go_debug" "$@"; then
      return 0
    fi
    if [[ "$attempt" == "$copy_attempts" ]]; then
      echo "$description failed after $copy_attempts attempts" >&2
      return 1
    fi
    echo "$description attempt $attempt/$copy_attempts failed; retrying in ${copy_retry_delay}s" >&2
    sleep "$copy_retry_delay"
  done
}

target_repository="${target_image%%@*}"
target_last_component="${target_repository##*/}"
if [[ "$target_last_component" == *:* ]]; then
  target_repository="${target_repository%:*}"
fi

target_sources=()
for i in "${!sources[@]}"; do
  source_ref="${sources[$i]}"
  digest="${source_digests[$i]}"
  retry "copying $source_ref to $target_image" \
    crane copy --jobs 1 "$source_ref" "$target_image"
  target_sources+=("${target_repository}@${digest}")
done

if [[ "${#target_sources[@]}" -gt 1 ]]; then
  index_args=()
  for target_source in "${target_sources[@]}"; do
    index_args+=(--manifest "$target_source")
  done
  retry "publishing runtime index $target_image" \
    crane index append --tag "$target_image" "${index_args[@]}"
fi
docker buildx imagetools inspect "$target_image" >/dev/null
echo "published $target_image from $source_image"
