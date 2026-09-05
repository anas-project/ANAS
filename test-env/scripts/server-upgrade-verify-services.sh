#!/usr/bin/env bash
# Print the number of ready containers in the active upgrade suite. Long-lived
# services must be running and either healthy or have no Docker healthcheck;
# an explicitly named *_init container may instead have completed successfully.
set -euo pipefail

: "${ANAS_TEST_CONTAINER_PREFIX:?ANAS_TEST_CONTAINER_PREFIX is required}"
# Nextcloud's declared 900-second start period plus its retry window reaches
# 1080 seconds. The default must outlive every in-tree healthcheck contract;
# callers can still lower it explicitly for unit tests or targeted diagnostics.
timeout=${ANAS_UPGRADE_READY_TIMEOUT_SECONDS:-1200}
interval=${ANAS_UPGRADE_READY_INTERVAL_SECONDS:-2}
[[ "$timeout" =~ ^[0-9]+$ ]] || { echo "invalid upgrade readiness timeout: $timeout" >&2; exit 2; }
[[ "$interval" =~ ^[1-9][0-9]*$ ]] || { echo "invalid upgrade readiness interval: $interval" >&2; exit 2; }

deadline=$((SECONDS + timeout))
last_problem=
while true; do
  listing=$(docker ps -a --format '{{.Names}}') || {
    echo "cannot list upgrade service containers" >&2
    exit 1
  }
  containers=()
  while IFS= read -r container; do
    [[ -n "$container" ]] || continue
    case "$container" in "$ANAS_TEST_CONTAINER_PREFIX"*) containers+=("$container") ;; esac
  done <<<"$listing"

  if [[ ${#containers[@]} -eq 0 ]]; then
    last_problem="no upgrade service containers were found"
  else
    last_problem=
    for container in "${containers[@]}"; do
      state=$(docker inspect --format '{{.State.Status}}' "$container")
      health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container")
      exit_code=$(docker inspect --format '{{.State.ExitCode}}' "$container")
      restart=$(docker inspect --format '{{.HostConfig.RestartPolicy.Name}}' "$container")
      if [[ "$state" == running && ( "$health" == healthy || "$health" == none ) ]]; then
        continue
      fi
      if [[ "$container" == *_init && "$state" == exited && "$exit_code" == 0 && "$restart" == no ]]; then
        continue
      fi
      last_problem="upgrade service is not ready: $container state=$state health=$health exit=$exit_code restart=$restart"
      break
    done
  fi

  if [[ -z "$last_problem" ]]; then
    printf '%s\n' "${#containers[@]}"
    exit 0
  fi
  if (( SECONDS >= deadline )); then
    echo "$last_problem" >&2
    exit 1
  fi
  sleep "$interval"
done
