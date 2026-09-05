#!/usr/bin/env bash
set -euo pipefail

. "$(dirname -- "$0")/server-require-isolated-docker.sh"
: "${ANAS_UPGRADE_WORKSPACE:?ANAS_UPGRADE_WORKSPACE is required}"
. "$(dirname -- "$0")/server-upgrade-export-runtime.sh"
services=$("$(dirname -- "$0")/server-upgrade-verify-services.sh")
marker_file="$ANAS_UPGRADE_WORKSPACE/.anas/upgrade-e2e-markers"
[[ -s "$marker_file" ]] || { echo "upgrade marker inventory is missing" >&2; exit 1; }

count=0
while IFS=$'\t' read -r container path marker; do
  [[ -n "$container" && -n "$path" && -n "$marker" ]]
  actual=$(docker exec "$container" sh -ceu 'cat "$1"' sh "$path")
  [[ "$actual" == "$marker" ]] || {
    echo "persistent marker changed in $container:$path" >&2
    exit 1
  }
  state=$(docker inspect --format '{{.State.Status}}' "$container")
  health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container")
  [[ "$state" == running && "$health" != unhealthy ]]
  count=$((count + 1))
done <"$marker_file"

printf 'upgrade_verify=pass phase=%s markers=%s services=%s\n' \
  "${ANAS_UPGRADE_PHASE:-unknown}" "$count" "$services"
