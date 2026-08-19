#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

config=${ANAS_TEST_CONFIG:-$CONFIG_DIR/full.yml}
ws=${ANAS_TEST_WORKSPACE:-$RUNTIME_DIR/full}
wait_seconds=${ANAS_SMOKE_WAIT_SECONDS:-180}
started=1

# The repository's full smoke fixture intentionally contains non-working DNS
# vendor credentials. ddns-updater's image healthcheck validates the external
# record update itself, so it is expected to be unhealthy in that one fixture
# even when the process and UI are stable. Real provider updates belong to the
# credentialed DDNS E2E suite.
expected_external_health_failure() {
  [ "$1" = "anas_ddns_updater" ] && grep -q 'cloudflare_dns_api_token: test-token-not-a-real-credential' "$config"
}

compose_project() {
  module_dir=$1
  module_name=$2
  prefix=$(sed -n 's/^CONTAINER_PREFIX=//p' "$module_dir/.env" | head -1)
  printf '%s%s\n' "${prefix:-anas_}" "$module_name"
}

cleanup() {
  if [ "$started" -eq 1 ]; then
    if [ -f "$ws/.anas/state/active.yml" ]; then
      run_anas stop -w "$ws" >>"$REPORT_DIR/smoke-stop.log" 2>&1 || true
    else
      find "$(ws_deployments "$ws")" -name docker-compose.yml 2>/dev/null | sort -r | while read -r compose_file; do
        module_dir=$(dirname "$compose_file")
        module_name=$(basename "$module_dir")
        project_name=$(compose_project "$module_dir" "$module_name")
        docker compose --project-name "$project_name" -f "$compose_file" --env-file "$module_dir/.env" down --remove-orphans >>"$REPORT_DIR/smoke-stop.log" 2>&1 || true
      done
      docker network rm anas_macvlan >>"$REPORT_DIR/smoke-stop.log" 2>&1 || true
    fi
  fi
}
trap cleanup EXIT INT TERM

make_workspace "$ws" "$config"
if run_anas apply --build -w "$ws" --update-lock >"$REPORT_DIR/smoke-start.log" 2>&1; then
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
    exit_code=$(docker inspect --format '{{.State.ExitCode}}' "$container_id")
    restart_policy=$(docker inspect --format '{{.HostConfig.RestartPolicy.Name}}' "$container_id")
    name=$(docker inspect --format '{{.Name}}' "$container_id" | sed 's|^/||')
    health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container_id")
    if [ "$state" = "exited" ] && [ "$exit_code" -eq 0 ] && [ "$restart_policy" = "no" ]; then
      continue
    fi
    if [ "$state" = "running" ] && [ "$health" = "unhealthy" ] && expected_external_health_failure "$name"; then
      continue
    fi
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
active_id=$(sed -n 's/^active_deployment: //p' "$ws/.anas/state/active.yml")
find "$(ws_deployments "$ws")/$active_id/modules" -mindepth 2 -maxdepth 2 -name docker-compose.yml | sort >"$compose_list"

while read -r compose_file; do
  module_dir=$(dirname "$compose_file")
  module_name=$(basename "$module_dir")
  project_name=$(compose_project "$module_dir" "$module_name")
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
    exit_code=$(docker inspect --format '{{.State.ExitCode}}' "$container_id")
    restart_policy=$(docker inspect --format '{{.HostConfig.RestartPolicy.Name}}' "$container_id")
    restarts=$(docker inspect --format '{{.RestartCount}}' "$container_id")
    health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container_id")
    name=$(docker inspect --format '{{.Name}}' "$container_id" | sed 's|^/||')
    echo "$name state=$state health=$health restarts=$restarts" >>"$log"

    if [ "$state" = "exited" ] && [ "$exit_code" -eq 0 ] && [ "$restart_policy" = "no" ]; then
      continue
    fi
    if [ "$state" = "running" ] && [ "$restarts" -eq 0 ] && [ "$health" = "unhealthy" ] && expected_external_health_failure "$name"; then
      echo "$name external provider health skipped for fake smoke credential" >>"$log"
      continue
    fi
    if [ "$state" != "running" ] || [ "$restarts" -ne 0 ] || [ "$health" = "unhealthy" ] || [ "$health" = "starting" ]; then
      echo "container $name is not stable; see $log" >&2
      failed=1
    fi
  done
done <"$compose_list"

if run_anas stop -w "$ws" >"$REPORT_DIR/smoke-stop.log" 2>&1; then
  started=0
  cat "$REPORT_DIR/smoke-stop.log"
else
  status=$?
  cat "$REPORT_DIR/smoke-stop.log"
  exit "$status"
fi

exit "$failed"
