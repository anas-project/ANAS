#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

config="$CONFIG_DIR/full.yml"
base="$RUNTIME_DIR/full"
mkdir -p "$base"
runtime_config="$base/config.yml"
cp "$config" "$runtime_config"
deployment_id=$(run_anas render -c "$runtime_config" -b "$base" --update-lock 2>"$REPORT_DIR/compose-render.log")

find "$base/deployments/$deployment_id/casks" -mindepth 2 -maxdepth 2 -name docker-compose.yml | sort | while read -r compose_file; do
  module_dir=$(dirname "$compose_file")
  module_name=$(basename "$module_dir")
  log="$REPORT_DIR/compose-$module_name.log"
  echo "== docker compose config: $module_name =="
  docker compose -f "$compose_file" --env-file "$module_dir/.env" config >"$log"
done
