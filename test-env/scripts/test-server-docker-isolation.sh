#!/usr/bin/env sh
# TEST_CASES: TESTAUTO-T-015
set -eu

. "$(dirname -- "$0")/common.sh"

guard="$TEST_ENV_DIR/scripts/server-require-isolated-docker.sh"
fake_bin="$TEST_ENV_DIR/fakes"
log="$REPORT_DIR/server-docker-isolation.log"
mkdir -p "$REPORT_DIR"
: >"$log"

run_guard() {
  env -i PATH="$fake_bin:/usr/bin:/bin" ANAS_FAKE_DOCKER_LOG="$log" "$@" \
    sh -c '. "$1"' sh "$guard"
}

if run_guard; then
  echo "server E2E guard accepted an unset Docker host" >&2
  exit 1
fi
if run_guard DOCKER_HOST=unix:///var/run/docker.sock; then
  echo "server E2E guard accepted the production Docker socket" >&2
  exit 1
fi
if run_guard DOCKER_HOST=unix:///run/anas-e2e-r7.sock ANAS_FAKE_DOCKER_ROOT=/var/lib/docker; then
  echo "server E2E guard accepted a production data root behind a test-named socket" >&2
  exit 1
fi
run_guard DOCKER_HOST=unix:///run/anas-e2e-r7.sock ANAS_FAKE_DOCKER_ROOT=/data/anas-e2e-r7/docker

printf 'server Docker isolation guard test passed\n'
