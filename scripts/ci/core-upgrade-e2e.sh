#!/usr/bin/env bash
# Exercise an actual historical Core binary and let the worktree binary read
# and advance the exact workspace it created. This deliberately never starts
# Docker; service/data continuity belongs to the server Module suites.
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
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/anas-core-upgrade.XXXXXX")
old_source=$work_dir/old-source
old_bin=$work_dir/old-anas
new_bin=$work_dir/new-anas
workspace=$work_dir/workspace
config=$work_dir/config.yml

cleanup() {
  case "$work_dir" in
    "${TMPDIR:-/tmp}"/anas-core-upgrade.*|/tmp/anas-core-upgrade.*) rm -rf -- "$work_dir" ;;
    *) echo "refusing to remove unexpected path: $work_dir" >&2 ;;
  esac
}
trap cleanup EXIT HUP INT TERM

run_json() {
  local output=$1
  shift
  if ! "$@" >"$output"; then
    cat "$output" >&2 || true
    return 1
  fi
}

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

# traefik is intentionally small but still produces a real lock, immutable
# deployment artifact, generated secret state, and rendered Compose project.
printf '%s\n' \
  'modules:' \
  '  traefik: {}' \
  'global:' \
  '  base_domain: upgrade.test' \
  '  email: admin@upgrade.test' \
  '  timezone: Asia/Singapore' \
  '  virtual_domain: true' >"$config"

run_json "$work_dir/old-init.json" "$old_bin" init "$workspace" -c "$config" --module-root "$old_source/modules" -y --json
run_json "$work_dir/old-lock.json" "$old_bin" lock -w "$workspace" --module-root "$old_source/modules" --json
run_json "$work_dir/old-render.json" "$old_bin" render -w "$workspace" --module-root "$old_source/modules" --json
old_deployment=
if [[ -f "$workspace/.anas/state/active.yml" ]]; then
  old_deployment=$(sed -n 's/^active_deployment: //p' "$workspace/.anas/state/active.yml")
fi
if [[ -z "$old_deployment" ]]; then
  # render does not activate; take the sole immutable artifact it created.
  old_deployment=$(find "$workspace/.anas/deployments" -mindepth 1 -maxdepth 1 -type d -exec basename {} \;)
fi
[[ -n "$old_deployment" && "$old_deployment" != *$'\n'* ]]

config_digest=$(sha256sum "$workspace/config.yml" | awk '{print $1}')
run_json "$work_dir/new-inspect-old.json" "$new_bin" deployments inspect "$old_deployment" -w "$workspace" --json
run_json "$work_dir/new-lock.json" "$new_bin" lock -w "$workspace" --module-root "$repo_root/modules" --json
run_json "$work_dir/new-plan.json" "$new_bin" plan -w "$workspace" --module-root "$repo_root/modules" --json
run_json "$work_dir/new-render.json" "$new_bin" render -w "$workspace" --module-root "$repo_root/modules" --json
run_json "$work_dir/new-inspect-old-after.json" "$new_bin" deployments inspect "$old_deployment" -w "$workspace" --json

[[ "$(sha256sum "$workspace/config.yml" | awk '{print $1}')" == "$config_digest" ]]
[[ -f "$workspace/.anas/deployments/$old_deployment/deployment.yml" ]]
new_count=$(find "$workspace/.anas/deployments" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')
[[ "$new_count" -ge 2 ]]

printf 'core_upgrade=pass from_ref=%s from_commit=%s old_deployment=%s deployment_count=%s config_preserved=true\n' \
  "$from_ref" "$from_commit" "$old_deployment" "$new_count"
