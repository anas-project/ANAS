#!/usr/bin/env bash
set -euo pipefail

mode="${1:-validate}"
catalog=".github/images.json"
registry="${CNB_DOCKER_REGISTRY:-docker.cnb.cool}"
repo_slug="${CNB_REPO_SLUG_LOWERCASE:-anas.dev/anas}"

if [[ "$mode" != "validate" && "$mode" != "mirror-all" ]]; then
  echo "usage: $0 validate|mirror-all" >&2
  exit 2
fi

jq -e 'type == "array" and length > 0' "$catalog" >/dev/null
actual="$(find casks/mods -type f -name Dockerfile | LC_ALL=C sort)"
registered="$(jq -r '.[].dockerfile' "$catalog" | LC_ALL=C sort)"
if ! diff -u <(printf '%s\n' "$actual") <(printf '%s\n' "$registered"); then
  echo "Every Dockerfile must appear exactly once in $catalog" >&2
  exit 1
fi

while IFS=$'\t' read -r cask image; do
  manifest="casks/mods/${cask}/cask.yml"
  version="$(awk '$1 == "version:" { print $2; exit }' "$manifest")"
  revision="$(awk '$1 == "revision:" { print $2; exit }' "$manifest")"
  expected='${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/'"${image}:${version}-r${revision}"
  if ! grep -Fq "image: ${expected}" "casks/mods/${cask}/docker-compose.yml"; then
    echo "${cask}/docker-compose.yml must reference ${expected}" >&2
    exit 1
  fi
done < <(jq -r '.[] | [.cask, .image] | @tsv' "$catalog")

if [[ "$mode" == "validate" ]]; then
  echo "CNB image catalog and Compose references are valid."
  exit 0
fi

docker login -u "${CNB_TOKEN_USER_NAME}" -p "${CNB_TOKEN}" "$registry"

while IFS= read -r item; do
  cask="$(jq -r '.cask' <<<"$item")"
  image="$(jq -r '.image' <<<"$item")"
  manifest="casks/mods/${cask}/cask.yml"
  version="$(awk '$1 == "version:" { print $2; exit }' "$manifest")"
  revision="$(awk '$1 == "revision:" { print $2; exit }' "$manifest")"
  if [[ -z "$version" || ! "$revision" =~ ^[1-9][0-9]*$ ]]; then
    echo "$manifest must contain a version and a positive integer revision" >&2
    exit 1
  fi

  tag="${version}-r${revision}"
  source="ghcr.io/anas-project/${image}:${tag}"
  target="${registry}/${repo_slug}/${image}:${tag}"
  if docker buildx imagetools inspect "$target" >/dev/null 2>&1; then
    echo "$target already exists; skipping immutable tag."
    continue
  fi
  if ! docker buildx imagetools inspect "$source" >/dev/null 2>&1; then
    echo "$source does not exist or is not readable" >&2
    exit 1
  fi
  docker buildx imagetools create --tag "$target" "$source"
done < <(jq -c '.[]' "$catalog")
