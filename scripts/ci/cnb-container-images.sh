#!/usr/bin/env bash
set -euo pipefail

mode="${1:-validate}"
catalog=".github/images.json"
mirror_catalog=".github/mirrors.json"
runtime_image_script="scripts/ci/runtime-image.sh"
registry="${CNB_DOCKER_REGISTRY:-docker.cnb.cool}"
repo_slug="${CNB_REPO_SLUG_LOWERCASE:-anas.dev/anas}"

if [[ "$mode" != "validate" && "$mode" != "mirror-all" ]]; then
  echo "usage: $0 validate|mirror-all" >&2
  exit 2
fi

jq -e 'type == "array" and length > 0' "$catalog" >/dev/null
actual="$(find modules -type f -name Dockerfile | LC_ALL=C sort)"
registered="$(jq -r '.[].dockerfile' "$catalog" | LC_ALL=C sort)"
if ! diff -u <(printf '%s\n' "$actual") <(printf '%s\n' "$registered"); then
  echo "Every Dockerfile must appear exactly once in $catalog" >&2
  exit 1
fi

while IFS=$'\t' read -r module image; do
  manifest="modules/${module}/module.yml"
  version="$(awk '$1 == "version:" { print $2; exit }' "$manifest")"
  revision="$(awk '$1 == "revision:" { print $2; exit }' "$manifest")"
  expected='${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/'"${image}:${version}-r${revision}"
  if ! grep -Fq "image: ${expected}" "modules/${module}/docker-compose.yml"; then
    echo "${module}/docker-compose.yml must reference ${expected}" >&2
    exit 1
  fi
done < <(jq -r '.[] | [.module, .image] | @tsv' "$catalog")

jq -e '
  type == "array" and length > 0 and
  all(.[ ];
    (.modules | type == "array" and length > 0) and
    (.image | test("^anas-mirror-[a-z0-9-]+$")) and
    (.tag | type == "string" and length > 0 and . != "latest") and
    (.source | type == "string" and length > 0) and
    (.digest | test("^sha256:[0-9a-f]{64}$")) and
    (.platforms | test("^linux/(amd64|arm64)(,linux/(amd64|arm64))*$"))
  ) and
  ([.[].image] | length == (unique | length))
' "$mirror_catalog" >/dev/null
while IFS= read -r item; do
  image="$(jq -r '.image' <<<"$item")"
  tag="$(jq -r '.tag' <<<"$item")"
  expected='${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/'"${image}:${tag}"
  expected_from='${ANAS_IMAGE_REGISTRY}/'"${image}:${tag}"
  while IFS= read -r module; do
    # A module may wrap the mirrored upstream image in its own published
    # runtime image (for example oauth2-proxy's local-first bootstrap). In
    # that case the immutable mirror belongs in the Dockerfile FROM rather
    # than as the Compose runtime image.
    if ! grep -Fq "image: ${expected}" "modules/${module}/docker-compose.yml" &&
       ! grep -R -Fq --include='Dockerfile' "FROM ${expected_from}" "modules/${module}"; then
      echo "${module} Compose or Dockerfile must reference ${expected}" >&2
      exit 1
    fi
  done < <(jq -r '.modules[]' <<<"$item")
done < <(jq -c '.[]' "$mirror_catalog")

if [[ "$mode" == "validate" ]]; then
  echo "CNB image catalog and Compose references are valid."
  exit 0
fi

printf '%s' "${CNB_TOKEN}" | docker login -u "${CNB_TOKEN_USER_NAME}" --password-stdin "$registry"

while IFS= read -r item; do
  module="$(jq -r '.module' <<<"$item")"
  image="$(jq -r '.image' <<<"$item")"
  manifest="modules/${module}/module.yml"
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
  platforms="$(jq -r '.platforms' <<<"$item")"
  bash "$runtime_image_script" copy "$source" "$target" "$platforms"
done < <(jq -c '.[]' "$catalog")

while IFS= read -r item; do
  image="$(jq -r '.image' <<<"$item")"
  tag="$(jq -r '.tag' <<<"$item")"
  platforms="$(jq -r '.platforms' <<<"$item")"
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
  bash "$runtime_image_script" copy "$source" "$target" "$platforms"
done < <(jq -c '.[]' "$mirror_catalog")
