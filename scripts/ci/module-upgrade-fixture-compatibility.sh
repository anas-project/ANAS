#!/usr/bin/env bash
# Prove every Module upgrade suite config is accepted by both exact release
# endpoints. This is a fast fixture gate; the Docker runner remains the E2E.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <from-git-ref>" >&2
  exit 2
fi

from_ref=$1
repo_root=$(git rev-parse --show-toplevel)
from_commit=$(git -C "$repo_root" rev-parse --verify "${from_ref}^{commit}") || {
  echo "upgrade base is not a commit: $from_ref" >&2
  exit 2
}
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/anas-module-upgrade-fixtures.XXXXXX")
old_source=$work_dir/old-source
old_bin=$work_dir/old-anas
new_bin=$work_dir/new-anas

cleanup() {
  case "$work_dir" in
    "${TMPDIR:-/tmp}"/anas-module-upgrade-fixtures.*|/tmp/anas-module-upgrade-fixtures.*)
      rm -rf -- "$work_dir"
      ;;
    *) echo "refusing to remove unexpected path: $work_dir" >&2 ;;
  esac
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$old_source"
git -C "$repo_root" archive "$from_commit" | tar -x -C "$old_source"

(
  cd "$old_source"
  go build -trimpath -o "$old_bin" ./cmd/anas
)
(
  cd "$repo_root"
  go build -trimpath -o "$new_bin" ./cmd/anas
)

configs=()
while IFS= read -r config; do
  configs+=("$config")
done < <(
  cd "$repo_root"
  go run ./cmd/check-upgrade-tests --print-module-configs
)
[[ ${#configs[@]} -gt 0 ]] || {
  echo "catalog contains no Module suite configs" >&2
  exit 1
}

run_init() {
  local endpoint=$1
  local binary=$2
  local module_root=$3
  local config=$4
  local name=${config##*/}
  name=${name%.yml}
  local workspace=$work_dir/workspaces/$endpoint-$name
  local output=$work_dir/$endpoint-$name.json
  if ! "$binary" init "$workspace" -c "$repo_root/$config" --module-root "$module_root" -y --json >"$output"; then
    cat "$output" >&2 || true
    return 1
  fi
  printf 'module_upgrade_fixture=pass endpoint=%s config=%s\n' "$endpoint" "$config"
}

for config in "${configs[@]}"; do
  run_init old "$old_bin" "$old_source/modules" "$config"
  run_init new "$new_bin" "$repo_root/modules" "$config"
done

printf 'module_upgrade_fixtures=pass from_ref=%s from_commit=%s configs=%s\n' \
  "$from_ref" "$from_commit" "${#configs[@]}"
