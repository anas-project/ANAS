#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <vSemVer> <assets-dir> <probe|wait>" >&2
  exit 2
fi

tag="$1"
assets_dir="$2"
mode="$3"
api_root="${CNB_API_ROOT:-https://api.cnb.cool}"
repository="${CNB_REPOSITORY:-anas.dev/ANAS}"
token="${CNB_TOKEN:-}"

semver_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'
[[ "$tag" =~ $semver_pattern ]] || {
  echo "invalid ANAS release tag: $tag" >&2
  exit 2
}
[[ "$mode" == probe || "$mode" == wait ]] || {
  echo "mode must be probe or wait" >&2
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
  sha256sum --check SHA256SUMS >/dev/null
)
assets=("${archives[@]}" "$assets_dir/SHA256SUMS")

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
accept="application/vnd.cnb.api+json"
release_url="${api_root%/}/${repository}/-/releases/tags/${tag}"
release_json="$work_dir/release.json"

get_release() {
  curl --silent --show-error --location \
    --header "Accept: $accept" \
    --header "Authorization: Bearer $token" \
    --output "$release_json" \
    --write-out '%{http_code}' \
    "$release_url"
}

verify_release() {
  local file name matches count asset_json local_hash remote_hash remote_url remote_file
  for file in "${assets[@]}"; do
    name="$(basename "$file")"
    matches="$(jq -c --arg name "$name" '[.assets[]? | select(.name == $name)]' "$release_json")"
    count="$(jq 'length' <<<"$matches")"
    if (( count == 0 )); then
      return 3
    elif (( count > 1 )); then
      echo "CNB Release $tag contains duplicate assets named $name" >&2
      return 1
    fi

    asset_json="$(jq -c '.[0]' <<<"$matches")"
    local_hash="$(sha256sum "$file" | awk '{print $1}')"
    remote_hash="$(jq -r 'select((.hash_algo // "" | ascii_downcase) == "sha256") | .hash_value // empty' <<<"$asset_json")"
    if [[ -n "$remote_hash" ]]; then
      [[ "$remote_hash" == "$local_hash" ]] || {
        echo "CNB Release asset $name has a different SHA-256" >&2
        return 1
      }
      continue
    fi

    remote_url="$(jq -er '.url' <<<"$asset_json")"
    remote_file="$work_dir/$name"
    curl --fail --silent --show-error --location \
      --header "Accept: $accept" \
      --header "Authorization: Bearer $token" \
      --output "$remote_file" \
      "$remote_url"
    remote_hash="$(sha256sum "$remote_file" | awk '{print $1}')"
    [[ "$remote_hash" == "$local_hash" ]] || {
      echo "CNB Release asset $name has different content" >&2
      return 1
    }
  done
}

attempts=1
[[ "$mode" == wait ]] && attempts=60
for (( attempt = 1; attempt <= attempts; attempt++ )); do
  status="$(get_release)"
  if [[ "$status" == 200 ]]; then
    if verify_release; then
      echo complete
      exit 0
    else
      result=$?
      (( result == 3 )) || exit "$result"
      state=incomplete
    fi
  elif [[ "$status" == 404 ]]; then
    state=missing
  else
    echo "CNB Release lookup failed with HTTP $status" >&2
    cat "$release_json" >&2
    exit 1
  fi

  if [[ "$mode" == probe ]]; then
    echo "$state"
    exit 0
  fi
  (( attempt < attempts )) && sleep 5
done

echo "CNB Release $tag did not become complete within five minutes" >&2
exit 1
