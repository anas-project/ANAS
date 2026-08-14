#!/usr/bin/env sh
# End-to-end rollback tests that need no Btrfs.
#
# R1  A configuration-only rollback reverts the artifact and KEEPS the data.
# R10 A rollback that fails part-way leaves the services running as before.
#
# R1 is the reason `anas rollback` exists as something separate from restoring
# a snapshot. The most common rollback is "I broke the config, the data is
# fine" — answering that with a snapshot restore would throw away every byte
# written since the last apply. Nothing in the code currently holds that
# guarantee in place, which is what this test is for.
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

ws=${ANAS_ROLLBACK_WORKSPACE:-$RUNTIME_DIR/rollback}
config=${ANAS_ROLLBACK_CONFIG:-$CONFIG_DIR/rollback.yml}
# Must match CONTAINER_PREFIX in the config: the test may run on a host that is
# already running a real deployment, and matching `anas_` here would make the
# assertions read the live containers.
prefix=${ANAS_ROLLBACK_PREFIX:-anasrb_}
log="$REPORT_DIR/rollback.log"
marker="anas-rollback-marker"
failures=0

cleanup() {
  run_anas stop -w "$ws" >>"$log" 2>&1 || true
}
trap cleanup EXIT INT TERM

fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

active_deployment() {
  sed -n 's/^active_deployment: //p' "$ws/.anas/state/active.yml"
}

running_containers() {
  docker ps --filter "name=^$prefix" --format '{{.Names}}' | sort
}

{
  echo "== R1: set up baseline =="
  make_workspace "$ws" "$config"
  run_anas apply --build -w "$ws" --update-lock
  first=$(active_deployment)
  echo "baseline deployment: $first"

  if [ -z "$(running_containers)" ]; then
    fail "no ${prefix}* containers running after the first apply"
  fi

  echo "== R1: write data that must survive the rollback =="
  # Written into the live data directory, not into a container-private path, so
  # the assertion holds regardless of which containers a config brings up.
  date -u +%Y-%m-%dT%H:%M:%SZ >"$ws/data/$marker"

  echo "== R1: change configuration only, no version change =="
  run_anas config set global.timezone Europe/Berlin -w "$ws"
  second=$(active_deployment)
  echo "second deployment: $second"
  if [ "$first" = "$second" ]; then
    fail "config set did not produce a new deployment"
  fi

  echo "== R1: roll back =="
  run_anas rollback -w "$ws"
  reverted=$(active_deployment)

  if [ "$reverted" != "$first" ]; then
    fail "rollback left $reverted active, expected $first"
  fi
  # The whole point of R1: the artifact went back, the data did not.
  if [ ! -f "$ws/data/$marker" ]; then
    fail "rollback destroyed data written after the previous apply"
  fi
  # And a config-only rollback must not need --allow-risky.
  echo "R1 checks complete"

  echo "== R10: a failing rollback must leave the services running =="
  running_before=$(running_containers)
  # A deployment id that cannot exist: the rollback must refuse it and change
  # nothing. Any failure mode that stops containers on the way out would leave
  # the deployment down with no operator action, which is the one outcome
  # rollback is never allowed to produce.
  if run_anas rollback no-such-deployment-id -w "$ws" >/dev/null 2>&1; then
    fail "rollback accepted a nonexistent deployment id"
  fi
  running_after=$(running_containers)
  if [ "$running_before" != "$running_after" ]; then
    fail "a failed rollback changed which containers were running"
    printf 'before:\n%s\nafter:\n%s\n' "$running_before" "$running_after" >&2
  fi
  if [ "$(active_deployment)" != "$first" ]; then
    fail "a failed rollback moved the active pointer"
  fi
  echo "R10 checks complete"
} >"$log" 2>&1 || {
  status=$?
  cat "$log"
  echo "rollback test aborted with status $status" >&2
  exit "$status"
}

cat "$log"

if [ "$failures" -ne 0 ]; then
  echo "$failures rollback assertion(s) failed" >&2
  exit 1
fi
echo "rollback tests passed"
