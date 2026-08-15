#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 <vSemVer> <commit> <assets-dir> <true|false>" >&2
  exit 2
fi

tag="$1"
commit="$2"
assets_dir="$3"
prerelease="$4"
api_root="${CNB_API_ROOT:-https://api.cnb.cool}"
repository="${CNB_REPOSITORY:-anas.dev/ANAS}"
token="${CNB_TOKEN:-}"

semver_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'
[[ "$tag" =~ $semver_pattern ]] || {
  echo "invalid ANAS release tag: $tag" >&2
  exit 2
}
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || {
  echo "invalid release commit: $commit" >&2
  exit 2
}
[[ "$prerelease" == true || "$prerelease" == false ]] || {
  echo "prerelease must be true or false" >&2
  exit 2
}
[[ -n "$token" ]] || {
  echo "CNB_TOKEN is required" >&2
  exit 2
}
[[ -d "$assets_dir" && -f "$assets_dir/SHA256SUMS" ]] || {
  echo "assets directory must contain SHA256SUMS: $assets_dir" >&2
  exit 2
}

for command_name in curl jq sha256sum; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "$command_name is required" >&2
    exit 2
  }
done

shopt -s nullglob
archives=("$assets_dir"/*.tar.gz)
(( ${#archives[@]} > 0 )) || {
  echo "assets directory has no tar.gz archives: $assets_dir" >&2
  exit 2
}
(
  cd "$assets_dir"
  sha256sum --check SHA256SUMS
)
assets=("${archives[@]}" "$assets_dir/SHA256SUMS")

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
accept="application/vnd.cnb.api+json"
release_by_tag="${api_root%/}/${repository}/-/releases/tags/${tag}"
releases_url="${api_root%/}/${repository}/-/releases"

api_get_release() {
  local output="$1"
  curl --silent --show-error --location \
    --header "Accept: $accept" \
    --header "Authorization: Bearer $token" \
    --output "$output" \
    --write-out '%{http_code}' \
    "$release_by_tag"
}

release_json="$work_dir/release.json"
status="$(api_get_release "$release_json")"
if [[ "$status" == 404 ]]; then
  make_latest=true
  [[ "$prerelease" == true ]] && make_latest=false
  jq -n \
    --arg tag "$tag" \
    --arg commit "$commit" \
    --arg name "ANAS ${tag#v}" \
    --arg body "Linux amd64 and arm64 binaries mirrored byte-for-byte from the GitHub Release." \
    --arg make_latest "$make_latest" \
    --argjson prerelease "$prerelease" \
    '{tag_name: $tag, target_commitish: $commit, name: $name, body: $body,
      draft: false, prerelease: $prerelease, make_latest: $make_latest}' \
    >"$work_dir/create.json"
  status="$(
    curl --silent --show-error --location \
      --request POST \
      --header "Accept: $accept" \
      --header "Authorization: Bearer $token" \
      --header 'Content-Type: application/json' \
      --data-binary "@$work_dir/create.json" \
      --output "$release_json" \
      --write-out '%{http_code}' \
      "$releases_url"
  )"
  [[ "$status" == 201 ]] || {
    echo "CNB Release creation failed with HTTP $status" >&2
    cat "$release_json" >&2
    exit 1
  }
elif [[ "$status" != 200 ]]; then
  echo "CNB Release lookup failed with HTTP $status" >&2
  cat "$release_json" >&2
  exit 1
fi

release_id="$(jq -er '.id' "$release_json")"
release_commit="$(jq -r '.tag_commitish // empty' "$release_json")"
if [[ "$release_commit" =~ ^[0-9a-f]{40}$ && "$release_commit" != "$commit" ]]; then
  echo "CNB Release $tag identifies $release_commit, expected $commit" >&2
  exit 1
fi

verify_existing_asset() {
  local file="$1"
  local asset_json="$2"
  local name local_hash remote_hash remote_url remote_file
  name="$(basename "$file")"
  local_hash="$(sha256sum "$file" | awk '{print $1}')"
  remote_hash="$(jq -r 'select((.hash_algo // "" | ascii_downcase) == "sha256") | .hash_value // empty' <<<"$asset_json")"
  if [[ -n "$remote_hash" ]]; then
    [[ "$remote_hash" == "$local_hash" ]] || {
      echo "CNB Release asset $name already exists with a different SHA-256" >&2
      return 1
    }
    return 0
  fi

  remote_url="$(jq -er '.url' <<<"$asset_json")"
  remote_file="$work_dir/existing-$name"
  curl --fail --silent --show-error --location \
    --header "Accept: $accept" \
    --header "Authorization: Bearer $token" \
    --output "$remote_file" \
    "$remote_url"
  remote_hash="$(sha256sum "$remote_file" | awk '{print $1}')"
  [[ "$remote_hash" == "$local_hash" ]] || {
    echo "CNB Release asset $name already exists with different content" >&2
    return 1
  }
}

upload_asset() {
  local file="$1"
  local name size upload_response upload_status upload_url verify_url
  name="$(basename "$file")"
  size="$(wc -c <"$file" | tr -d ' ')"
  jq -n \
    --arg name "$name" \
    --argjson size "$size" \
    '{asset_name: $name, size: $size, overwrite: false, ttl: 0}' \
    >"$work_dir/upload-request.json"
  upload_response="$work_dir/upload-response.json"
  upload_status="$(
    curl --silent --show-error --location \
      --request POST \
      --header "Accept: $accept" \
      --header "Authorization: Bearer $token" \
      --header 'Content-Type: application/json' \
      --data-binary "@$work_dir/upload-request.json" \
      --output "$upload_response" \
      --write-out '%{http_code}' \
      "${releases_url}/${release_id}/asset-upload-url"
  )"
  [[ "$upload_status" == 201 ]] || {
    echo "failed to request an upload URL for $name: HTTP $upload_status" >&2
    cat "$upload_response" >&2
    return 1
  }

  upload_url="$(jq -er '.upload_url' "$upload_response")"
  verify_url="$(jq -er '.verify_url' "$upload_response")"
  case "$verify_url" in
    http://*|https://*) ;;
    /*) verify_url="${api_root%/}${verify_url}" ;;
    *) verify_url="${api_root%/}/${verify_url}" ;;
  esac
  curl --fail --silent --show-error \
    --request PUT \
    --header 'Content-Type: application/octet-stream' \
    --upload-file "$file" \
    "$upload_url"
  curl --fail --silent --show-error --location \
    --request POST \
    --header "Accept: $accept" \
    --header "Authorization: Bearer $token" \
    "$verify_url"
  echo "Uploaded $name to CNB Release $tag"
}

for file in "${assets[@]}"; do
  name="$(basename "$file")"
  matches="$(jq -c --arg name "$name" '[.assets[]? | select(.name == $name)]' "$release_json")"
  count="$(jq 'length' <<<"$matches")"
  if (( count > 1 )); then
    echo "CNB Release $tag contains duplicate assets named $name" >&2
    exit 1
  elif (( count == 1 )); then
    verify_existing_asset "$file" "$(jq -c '.[0]' <<<"$matches")"
    echo "Verified existing CNB Release asset $name"
  else
    upload_asset "$file"
  fi
done

complete=false
for attempt in {1..10}; do
  status="$(api_get_release "$release_json")"
  [[ "$status" == 200 ]] || {
    echo "CNB Release verification failed with HTTP $status" >&2
    exit 1
  }
  missing=false
  for file in "${assets[@]}"; do
    name="$(basename "$file")"
    if ! jq -e --arg name "$name" '.assets[]? | select(.name == $name)' "$release_json" >/dev/null; then
      missing=true
      break
    fi
  done
  if [[ "$missing" == false ]]; then
    complete=true
    break
  fi
  (( attempt < 10 )) && sleep 2
done
[[ "$complete" == true ]] || {
  echo "CNB Release $tag did not expose every uploaded attachment in time" >&2
  exit 1
}
for file in "${assets[@]}"; do
  name="$(basename "$file")"
  asset_json="$(jq -cer --arg name "$name" '.assets[] | select(.name == $name)' "$release_json")"
  verify_existing_asset "$file" "$asset_json"
done

echo "CNB Release $tag is complete and byte-identical to the GitHub assets."
