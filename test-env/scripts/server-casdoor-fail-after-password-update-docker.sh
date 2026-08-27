#!/usr/bin/env bash
set -euo pipefail

: "${ANAS_TEST_REAL_DOCKER:?ANAS_TEST_REAL_DOCKER is required}"
: "${ANAS_TEST_FAIL_ONCE_MARKER:?ANAS_TEST_FAIL_ONCE_MARKER is required}"
: "${ANAS_TEST_FAIL_CONTAINER:?ANAS_TEST_FAIL_CONTAINER is required}"

if [ "${1:-}" = exec ] && [ "${2:-}" = -i ] &&
  [ "${3:-}" = "$ANAS_TEST_FAIL_CONTAINER" ] &&
  [ "${4:-}" = /opt/anas/bin/casdoor-helper ] &&
  [ "${5:-}" = set-password ] &&
  [ ! -e "$ANAS_TEST_FAIL_ONCE_MARKER" ]; then
  "$ANAS_TEST_REAL_DOCKER" "$@"
  install -m 0600 /dev/null "$ANAS_TEST_FAIL_ONCE_MARKER"
  printf 'injected failure after Casdoor accepted the candidate password\n' >&2
  exit 97
fi

exec "$ANAS_TEST_REAL_DOCKER" "$@"
