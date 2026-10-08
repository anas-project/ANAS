#!/usr/bin/env bash
# TEST_CASES: TEMP-T-012, TEMP-T-017, TEMP-T-018, TEMP-T-019, TEMP-T-020, TEMP-T-024
set -euo pipefail
: "${ANAS_TEST_WORK_ROOT:?ANAS_TEST_WORK_ROOT is required}"
: "${ANAS_TEST_ANAS_CMD:?ANAS_TEST_ANAS_CMD is required}"
: "${ANAS_TEST_DOCKER_SOCKET:?ANAS_TEST_DOCKER_SOCKET is required}"
: "${ANAS_TEST_RUN_ID:?ANAS_TEST_RUN_ID is required}"
: "${ANAS_TEST_SOURCE_DIGEST:?ANAS_TEST_SOURCE_DIGEST is required}"
: "${ANAS_TEST_MOUNT_NAMESPACE:?ANAS_TEST_MOUNT_NAMESPACE is required}"
: "${ANAS_TEST_DOCKER_PID:?ANAS_TEST_DOCKER_PID is required}"
: "${ANAS_TEST_CONTAINERD_PID:?ANAS_TEST_CONTAINERD_PID is required}"
unset DOCKER_CONTEXT
export DOCKER_HOST="unix://$ANAS_TEST_DOCKER_SOCKET"
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# Verify the caller, persistent holder, Docker and containerd share the run's
# private mount namespace. A loop mount in the host namespace is never allowed.
python3 "$script_dir/server-workspace-temp-storage-extended-e2e.py" --verify-isolation
source "$script_dir/server-require-isolated-docker.sh"
exec python3 "$script_dir/server-workspace-temp-storage-extended-e2e.py" "$@"
