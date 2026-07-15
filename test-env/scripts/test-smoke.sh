#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

config=${ANAS_TEST_CONFIG:-$CONFIG_DIR/full.yml}
runtime=${ANAS_TEST_RUNTIME:-$RUNTIME_DIR/full}
wait_seconds=${ANAS_SMOKE_WAIT_SECONDS:-180}
started=1

cleanup() {
  if [ "$started" -eq 1 ]; then
    if [ -f "$runtime/release/config.yml" ]; then
      run_anas stop -b "$runtime" >>"$REPORT_DIR/smoke-stop.log" 2>&1 || true
    else
      find "$runtime/tmp" -mindepth 2 -maxdepth 2 -name docker-compose.yml 2>/dev/null | sort -r | while read -r compose_file; do
        module_dir=$(dirname "$compose_file")
        docker compose -f "$compose_file" --env-file "$module_dir/.env" down --remove-orphans >>"$REPORT_DIR/smoke-stop.log" 2>&1 || true
      done
      docker network rm anas_macvlan >>"$REPORT_DIR/smoke-stop.log" 2>&1 || true
    fi
  fi
}
trap cleanup EXIT INT TERM

if run_anas start --build -c "$config" -b "$runtime" >"$REPORT_DIR/smoke-start.log" 2>&1; then
  cat "$REPORT_DIR/smoke-start.log"
else
  status=$?
  cat "$REPORT_DIR/smoke-start.log"
  exit "$status"
fi

elapsed=0
poll_interval=10
while [ "$elapsed" -lt "$wait_seconds" ]; do
  stable=1
  container_ids=$(docker ps --all --quiet)
  if [ -z "$container_ids" ]; then
    stable=0
  fi
  for container_id in $container_ids; do
    state=$(docker inspect --format '{{.State.Status}}' "$container_id")
    health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container_id")
    if [ "$state" != "running" ] || [ "$health" = "starting" ] || [ "$health" = "unhealthy" ]; then
      stable=0
      break
    fi
  done
  if [ "$stable" -eq 1 ]; then
    echo "all containers became stable after ${elapsed}s"
    break
  fi
  sleep "$poll_interval"
  elapsed=$((elapsed + poll_interval))
done

failed=0
compose_list="$REPORT_DIR/smoke-compose-files.txt"
find "$runtime/release" -mindepth 2 -maxdepth 2 -name docker-compose.yml | sort >"$compose_list"

while read -r compose_file; do
  module_dir=$(dirname "$compose_file")
  module_name=$(basename "$module_dir")
  project_name="anas_$module_name"
  log="$REPORT_DIR/smoke-ps-$module_name.log"
  if ! docker compose --project-name "$project_name" -f "$compose_file" --env-file "$module_dir/.env" ps --all >"$log" 2>&1; then
    echo "module $module_name status check failed; see $log" >&2
    failed=1
    continue
  fi

  if ! container_ids=$(docker compose --project-name "$project_name" -f "$compose_file" --env-file "$module_dir/.env" ps --all --quiet 2>>"$log"); then
    echo "module $module_name container lookup failed; see $log" >&2
    failed=1
    continue
  fi
  if [ -z "$container_ids" ]; then
    echo "module $module_name has no containers; see $log" >&2
    failed=1
    continue
  fi

  for container_id in $container_ids; do
    state=$(docker inspect --format '{{.State.Status}}' "$container_id")
    restarts=$(docker inspect --format '{{.RestartCount}}' "$container_id")
    health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container_id")
    name=$(docker inspect --format '{{.Name}}' "$container_id" | sed 's|^/||')
    echo "$name state=$state health=$health restarts=$restarts" >>"$log"

    if [ "$state" != "running" ] || [ "$restarts" -ne 0 ] || [ "$health" = "unhealthy" ] || [ "$health" = "starting" ]; then
      echo "container $name is not stable; see $log" >&2
      failed=1
    fi
  done
done <"$compose_list"

if run_anas stop -b "$runtime" >"$REPORT_DIR/smoke-stop.log" 2>&1; then
  started=0
  cat "$REPORT_DIR/smoke-stop.log"
else
  status=$?
  cat "$REPORT_DIR/smoke-stop.log"
  exit "$status"
fi

exit "$failed"
