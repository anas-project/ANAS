#!/usr/bin/env bash
# TEST_CASES: TEMP-T-021
set -euo pipefail
: "${ANAS_TEST_WORK_ROOT:?ANAS_TEST_WORK_ROOT is required}"
: "${ANAS_TEST_RUN_ID:?ANAS_TEST_RUN_ID is required}"
: "${ANAS_TEST_ANAS_CMD:?ANAS_TEST_ANAS_CMD is required}"
: "${ANAS_TEST_MODULE_ROOT:?ANAS_TEST_MODULE_ROOT is required}"
: "${ANAS_TEST_DOCKER_SOCKET:?ANAS_TEST_DOCKER_SOCKET is required}"
: "${ANAS_TEST_SOURCE_DIGEST:?ANAS_TEST_SOURCE_DIGEST is required}"
: "${ANAS_TEST_ENTRY_IP:?ANAS_TEST_ENTRY_IP is required}"
: "${ANAS_TEST_NETWORK_NAMESPACE:?ANAS_TEST_NETWORK_NAMESPACE is required}"
unset DOCKER_CONTEXT
export DOCKER_HOST="unix://$ANAS_TEST_DOCKER_SOCKET"
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"
exec python3 "$script_dir/server-workspace-temp-storage-editing-e2e.py" "${@:-prepare}"
