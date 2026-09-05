#!/usr/bin/env bash
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
helper=$script_dir/server-upgrade-export-runtime.sh
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/anas-upgrade-runtime-test.XXXXXX")

cleanup() {
  case "$work_dir" in
    "${TMPDIR:-/tmp}"/anas-upgrade-runtime-test.*|/tmp/anas-upgrade-runtime-test.*) rm -rf -- "$work_dir" ;;
    *) echo "refusing to remove unexpected path: $work_dir" >&2 ;;
  esac
}
trap cleanup EXIT HUP INT TERM

workspace=$work_dir/workspace
deployment=deployment-test
module_dir=$workspace/.anas/deployments/$deployment/modules/traefik
mkdir -p "$workspace/.anas/state" "$module_dir"
printf 'active_deployment: %s\n' "$deployment" >"$workspace/.anas/state/active.yml"
printf '%s\n' \
  'CONTAINER_PREFIX=anas_upgrade_test_' \
  'HOST_IP=192.0.2.20' >"$module_dir/.env"

DOCKER_HOST=unix:///run/anas-e2e.sock ANAS_UPGRADE_WORKSPACE=$workspace HELPER=$helper \
  bash -ceu '
    source "$HELPER"
    [[ "$ANAS_TEST_DOCKER_SOCKET" == /run/anas-e2e.sock ]]
    [[ "$ANAS_TEST_CONTAINER_PREFIX" == anas_upgrade_test_ ]]
    [[ "$ANAS_TEST_ENTRY_IP" == 192.0.2.20 ]]
  '

printf 'upgrade_runtime_export_test=pass source=active-deployment\n'
