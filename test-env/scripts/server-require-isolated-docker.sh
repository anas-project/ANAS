#!/usr/bin/env sh
# Source this guard before any server E2E touches Docker. A distinctive socket
# name is necessary but not sufficient: the daemon must also report a dedicated
# test data root, so a symlink to the production socket cannot bypass the check.

anas_test_docker_host=${DOCKER_HOST:-${ANAS_TEST_DOCKER_HOST:-}}
if [ -z "$anas_test_docker_host" ] && [ -n "${ANAS_TEST_DOCKER_SOCKET:-}" ]; then
  anas_test_docker_host="unix://$ANAS_TEST_DOCKER_SOCKET"
fi

anas_isolation_fail() {
  printf 'refusing server E2E without an isolated Docker daemon: %s\n' "$1" >&2
  exit 2
}

case "$anas_test_docker_host" in
  unix:///run/docker.sock|unix:///var/run/docker.sock|"")
    anas_isolation_fail "DOCKER_HOST=${anas_test_docker_host:-<unset>}"
    ;;
  unix:///*)
    case "${anas_test_docker_host#unix://}" in
      *anas*test*|*anas*e2e*|*anas*anchor*) ;;
      *) anas_isolation_fail "socket name does not identify an ANAS test scope: $anas_test_docker_host" ;;
    esac
    ;;
  *) anas_isolation_fail "only an explicit isolated unix socket is accepted: $anas_test_docker_host" ;;
esac

export DOCKER_HOST=$anas_test_docker_host
anas_test_docker_root=$(${DOCKER_CMD:-docker} info --format '{{.DockerRootDir}}' 2>/dev/null) ||
  anas_isolation_fail "cannot inspect $DOCKER_HOST"
case "$anas_test_docker_root" in
  *anas*test*|*anas*e2e*|*anas*anchor*) ;;
  *) anas_isolation_fail "daemon data root is not test-scoped: ${anas_test_docker_root:-<empty>}" ;;
esac

unset anas_test_docker_host anas_test_docker_root
