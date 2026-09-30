#!/usr/bin/env bash
# TEST_CASES: NCT-T-002
set -euo pipefail

: "${ANAS_TEST_WORKSPACE:?ANAS_TEST_WORKSPACE is required}"
: "${ANAS_TEST_CONTAINER_PREFIX:?ANAS_TEST_CONTAINER_PREFIX is required}"
: "${ANAS_TEST_DOCKER_SOCKET:?ANAS_TEST_DOCKER_SOCKET is required}"
: "${ANAS_TEST_ENTRY_IP:?ANAS_TEST_ENTRY_IP is required}"
export DOCKER_HOST="unix://$ANAS_TEST_DOCKER_SOCKET"
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"
exec python3 "$script_dir/server-nextcloud-trashbin-e2e.py"
