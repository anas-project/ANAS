#!/usr/bin/env bash
set -euo pipefail

mode="${1:-changed}"
catalog=".github/images.json"
registry="${CNB_DOCKER_REGISTRY:-docker.cnb.cool}"
repo_slug="${CNB_REPO_SLUG_LOWERCASE:-anas.dev/anas}"

if [[ "$mode" != "validate" && "$mode" != "changed" && "$mode" != "all" ]]; then
  echo "usage: $0 validate|changed|all" >&2
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

selected="$(jq -c '.' "$catalog")"
base_sha=""
if [[ "$mode" == "changed" ]]; then
  new_commits="${CNB_NEW_COMMITS_COUNT:-}"
  if [[ "$new_commits" =~ ^[1-9][0-9]*$ ]]; then
    # A first mirror imports the complete history. In that case HEAD~N does not
    # exist because N includes the root commit, and an empty base deliberately
    # selects every image for the initial publication.
    if git rev-parse --verify "HEAD~${new_commits}" >/dev/null 2>&1; then
      base_sha="$(git rev-parse "HEAD~${new_commits}")"
    fi
  elif git rev-parse --verify HEAD^ >/dev/null 2>&1; then
    base_sha="$(git rev-parse HEAD^)"
  fi

  if [[ -n "$base_sha" ]]; then
    changed="$(git diff --name-only "$base_sha" HEAD)"
    selected='[]'
    while IFS= read -r item; do
      context="$(jq -r '.context' <<<"$item")"
      if grep -q "^${context}/" <<<"$changed"; then
        selected="$(jq -c --argjson item "$item" '. + [$item]' <<<"$selected")"
      fi
    done < <(jq -c '.[]' "$catalog")
  fi
fi

if [[ "$(jq 'length' <<<"$selected")" == "0" ]]; then
  echo "No cask image build context changed."
  exit 0
fi

if [[ -n "$base_sha" ]]; then
  while IFS= read -r cask; do
    manifest="casks/mods/${cask}/cask.yml"
    if ! old_manifest="$(git show "${base_sha}:${manifest}" 2>/dev/null)"; then
      continue
    fi
    old_version="$(awk '$1 == "version:" { print $2; exit }' <<<"$old_manifest")"
    old_revision="$(awk '$1 == "revision:" { print $2; exit }' <<<"$old_manifest")"
    old_revision="${old_revision:-0}"
    new_version="$(awk '$1 == "version:" { print $2; exit }' "$manifest")"
    new_revision="$(awk '$1 == "revision:" { print $2; exit }' "$manifest")"
    if [[ "$new_version" == "$old_version" ]]; then
      expected_revision=$((old_revision + 1))
      if [[ "$new_revision" != "$expected_revision" ]]; then
        echo "$cask container changed without revision $expected_revision" >&2
        exit 1
      fi
    elif [[ "$new_revision" != "1" ]]; then
      echo "$cask changed version from $old_version to $new_version, so revision must reset to 1" >&2
      exit 1
    fi
  done < <(jq -r '.[].cask' <<<"$selected" | LC_ALL=C sort -u)
fi

docker login -u "${CNB_TOKEN_USER_NAME}" -p "${CNB_TOKEN}" "$registry"

while IFS= read -r item; do
  cask="$(jq -r '.cask' <<<"$item")"
  image="$(jq -r '.image' <<<"$item")"
  context="$(jq -r '.context' <<<"$item")"
  dockerfile="$(jq -r '.dockerfile' <<<"$item")"
  platforms="$(jq -r '.platforms' <<<"$item")"
  manifest="casks/mods/${cask}/cask.yml"
  version="$(awk '$1 == "version:" { print $2; exit }' "$manifest")"
  revision="$(awk '$1 == "revision:" { print $2; exit }' "$manifest")"
  if [[ -z "$version" || ! "$revision" =~ ^[1-9][0-9]*$ ]]; then
    echo "$manifest must contain a version and a positive integer revision" >&2
    exit 1
  fi

  tag="${version}-r${revision}"
  target="${registry}/${repo_slug}/${image}:${tag}"
  cache="${registry}/${repo_slug}/${image}:buildcache"
  if docker buildx imagetools inspect "$target" >/dev/null 2>&1; then
    echo "$target already exists; published tags are immutable" >&2
    exit 1
  fi

  docker buildx build \
    --file "$dockerfile" \
    --platform "$platforms" \
    --tag "$target" \
    --label "org.opencontainers.image.source=https://cnb.cool/${CNB_REPO_SLUG}" \
    --label "org.opencontainers.image.revision=${CNB_COMMIT}" \
    --label "org.opencontainers.image.version=${tag}" \
    --label "dev.anas.cask=${cask}" \
    --label "dev.anas.cask.revision=${revision}" \
    --build-arg APT_MIRROR_URL=https://mirrors.aliyun.com \
    --build-arg APK_MIRROR_URL=https://mirrors.aliyun.com \
    --build-arg NPM_REGISTRY_URL=https://registry.npmmirror.com \
    --build-arg GOPROXY_URL=https://goproxy.cn,direct \
    --build-arg GITHUB_DOWNLOAD_PROXY_PREFIX=https://files.m.daocloud.io/ \
    --build-arg DOCKER_HUB_REGISTRY=m.daocloud.io/docker.io \
    --build-arg LLNG_DOCKER_HUB_REGISTRY=docker.1ms.run \
    --build-arg GHCR_REGISTRY=ghcr.nju.edu.cn \
    --build-arg QUAY_REGISTRY=quay.nju.edu.cn \
    --cache-from "type=registry,ref=${cache}" \
    --cache-to "type=registry,ref=${cache},mode=max" \
    --provenance mode=max \
    --sbom true \
    --push \
    "$context"
done < <(jq -c '.[]' <<<"$selected")
