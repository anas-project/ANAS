#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"

workspace=${ANAS_TEST_WORKSPACE:?ANAS_TEST_WORKSPACE is required}
anas_cmd=${ANAS_TEST_ANAS_CMD:-anas}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_vik_}
report_dir=${ANAS_TEST_REPORT_DIR:-$script_dir/../reports}
real_docker=${ANAS_TEST_REAL_DOCKER:-$(command -v docker)}
wrapper=${ANAS_TEST_DOCKER_WRAPPER:-$script_dir/server-vikunja-fail-once-docker.sh}
vikunja_container="${prefix}vikunja"
project="${prefix}vikunja"
run_dir=$(mktemp -d)
marker=$run_dir/fail-once.marker
bin_dir=$run_dir/bin
report=$report_dir/vikunja-rotation-failure.json
stderr_report=$report_dir/vikunja-rotation-failure.err

cleanup() {
  rm -rf "$run_dir"
}
trap cleanup EXIT HUP INT TERM
trap 'printf "FAIL: Vikunja rotation failure E2E line=%s\n" "$LINENO" >&2' ERR

active_deployment() {
  sed -n 's/^active_deployment: //p' "$workspace/.anas/state/active.yml"
}

store_digest() {
  sha256sum "$workspace/.anas/secrets.yml" | awk '{ print $1 }'
}

live_secret_digest() {
  "$real_docker" inspect --format '{{range .Config.Env}}{{println .}}{{end}}' \
    "$vikunja_container" |
    sed -n 's/^VIKUNJA_SERVICE_SECRET=//p' |
    sha256sum | awk '{ print $1 }'
}

install -d -m 0700 "$bin_dir" "$report_dir"
ln -s "$wrapper" "$bin_dir/docker"
chmod 0755 "$wrapper"
umask 077

before_deployment=$(active_deployment)
before_store=$(store_digest)
before_live=$(live_secret_digest)

if PATH="$bin_dir:$PATH" \
  ANAS_TEST_REAL_DOCKER="$real_docker" \
  ANAS_TEST_FAIL_ONCE_MARKER="$marker" \
  ANAS_TEST_FAIL_PROJECT="$project" \
    "$anas_cmd" credential rotate vikunja.service_secret -w "$workspace" -y --json \
    >"$report" 2>"$stderr_report"; then
  status=0
else
  status=$?
fi
chmod 0600 "$report" "$stderr_report"

test "$status" = 1
test -f "$marker"
jq -e '
  .ok == false and
  .error.code == "credential_rotation_failed" and
  .error.detail.rotation.status == "previous_restored" and
  (.error.detail.rotation.previous_deployment | length > 0) and
  (.error.detail.rotation.candidate_deployment | length > 0)
' "$report" >/dev/null
test "$(active_deployment)" = "$before_deployment"
test "$(store_digest)" = "$before_store"
test "$(live_secret_digest)" = "$before_live"
for attempt in $(seq 1 90); do
  state=$("$real_docker" inspect --format '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}' \
    "$vikunja_container" 2>/dev/null || true)
  [ "$state" = 'running|healthy' ] && break
  sleep 2
done
test "$state" = 'running|healthy'
test "$("$real_docker" inspect --format '{{.RestartCount}}' "$vikunja_container")" = 0

printf 'PASS: Vikunja rotation candidate failure restored previous deployment=%s store=unchanged live_secret=unchanged\n' \
  "$before_deployment"
