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

# Compose a runtime-only index. Provenance and SBOM descriptors use
# os/architecture=unknown and can exceed CNB's per-manifest metadata limit;
# they remain available on ANAS-built GHCR packages but are not deployment
# inputs and are deliberately omitted from mirrors.
docker buildx imagetools create --tag "$target_image" "${sources[@]}"
docker buildx imagetools inspect "$target_image" >/dev/null
echo "published $target_image from $source_image"
