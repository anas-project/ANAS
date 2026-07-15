#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

config="$CONFIG_DIR/full.yml"
base="$RUNTIME_DIR/full"
run_anas render -c "$config" -b "$base" >"$REPORT_DIR/compose-render.log" 2>&1

find "$base/release" -mindepth 2 -maxdepth 2 -name docker-compose.yml | sort | while read -r compose_file; do
  module_dir=$(dirname "$compose_file")
  module_name=$(basename "$module_dir")
  log="$REPORT_DIR/compose-$module_name.log"
  echo "== docker compose config: $module_name =="
  docker compose -f "$compose_file" --env-file "$module_dir/.env" config >"$log"
done
