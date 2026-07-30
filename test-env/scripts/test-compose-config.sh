#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

ws="$RUNTIME_DIR/full"
make_workspace "$ws" "$CONFIG_DIR/full.yml"
deployment_id=$(run_anas render -w "$ws" --update-lock 2>"$REPORT_DIR/compose-render.log")

find "$(ws_deployments "$ws")/$deployment_id/casks" -mindepth 2 -maxdepth 2 -name docker-compose.yml | sort | while read -r compose_file; do
  module_dir=$(dirname "$compose_file")
  module_name=$(basename "$module_dir")
  log="$REPORT_DIR/compose-$module_name.log"
  echo "== docker compose config: $module_name =="
  docker compose -f "$compose_file" --env-file "$module_dir/.env" config >"$log"
done
