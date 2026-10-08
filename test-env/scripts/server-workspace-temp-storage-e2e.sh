#!/usr/bin/env bash
# TEST_CASES: TEMP-T-012, TEMP-T-013, TEMP-T-014, TEMP-T-015, TEMP-T-016, TEMP-T-019
set -euo pipefail
: "${ANAS_TEST_WORK_ROOT:?ANAS_TEST_WORK_ROOT is required}"
: "${ANAS_TEST_ANAS_CMD:?ANAS_TEST_ANAS_CMD is required}"
: "${ANAS_TEST_DOCKER_SOCKET:?ANAS_TEST_DOCKER_SOCKET is required}"
: "${ANAS_TEST_RUN_ID:?ANAS_TEST_RUN_ID is required}"
unset DOCKER_CONTEXT
export DOCKER_HOST="unix://$ANAS_TEST_DOCKER_SOCKET"
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"
exec python3 "$script_dir/server-workspace-temp-storage-e2e.py" "${@:-core}"
