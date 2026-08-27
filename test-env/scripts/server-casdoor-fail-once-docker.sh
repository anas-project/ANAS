#!/usr/bin/env bash
set -euo pipefail

: "${ANAS_TEST_REAL_DOCKER:?ANAS_TEST_REAL_DOCKER is required}"
: "${ANAS_TEST_FAIL_ONCE_MARKER:?ANAS_TEST_FAIL_ONCE_MARKER is required}"
: "${ANAS_TEST_FAIL_PROJECT:?ANAS_TEST_FAIL_PROJECT is required}"

arguments=" $* "
if [ "${1:-}" = compose ] &&
  [[ "$arguments" == *" --project-name $ANAS_TEST_FAIL_PROJECT "* ]] &&
  [[ "$arguments" == *" up "* ]] &&
  [ ! -e "$ANAS_TEST_FAIL_ONCE_MARKER" ]; then
  install -m 0600 /dev/null "$ANAS_TEST_FAIL_ONCE_MARKER"
  printf 'injected one-shot compose up failure for project %s\n' \
    "$ANAS_TEST_FAIL_PROJECT" >&2
  exit 97
fi

exec "$ANAS_TEST_REAL_DOCKER" "$@"
