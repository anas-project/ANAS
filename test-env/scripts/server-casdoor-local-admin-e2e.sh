#!/usr/bin/env bash
# Casdoor break-glass login, rotation, and rollback E2E.
set -Eeuo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"

workspace=${ANAS_TEST_WORKSPACE:?ANAS_TEST_WORKSPACE is required}
anas_cmd=${ANAS_TEST_ANAS_CMD:-anas}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_casdoor_}
casdoor="${prefix}casdoor"
fixture_bin=${CASDOOR_LOGOUT_FIXTURE_BIN:-/home/whl/anas-casdoor-m3-e2e/casdoor-oidc-logout-consumer}
real_docker=${ANAS_TEST_REAL_DOCKER:-$(command -v docker)}
wrapper=${ANAS_TEST_DOCKER_WRAPPER:-$script_dir/server-casdoor-fail-after-password-update-docker.sh}
run_dir=$(mktemp -d)
bin_dir=$run_dir/bin
marker=$run_dir/fail-after-update.marker

cleanup() {
  rm -rf "$run_dir"
}
trap cleanup EXIT HUP INT TERM
trap 'printf "FAIL: Casdoor local-admin E2E line=%s\n" "$LINENO" >&2' ERR

credential() {
  "$anas_cmd" admin local credential casdoor break_glass -w "$workspace" --json
}

extract_credential() {
  local input=$1 password_file=$2
  jq -er '.account.password' "$input" >"$password_file"
  test "$(jq -r '.account.username' "$input")" = admin_casdoor
  test "$(jq -r '.account.module + ":" + .account.id' "$input")" = casdoor:break_glass
  chmod 0600 "$password_file"
}

admin_login() {
  local password_file=$1
  "$real_docker" run --rm -i --network "container:$casdoor" \
    --entrypoint /fixture -v "$fixture_bin:/fixture:ro" \
    -e CASDOOR_FIXTURE_ISSUER="$issuer" \
    -e CASDOOR_FIXTURE_INTERNAL_ORIGIN=http://127.0.0.1:8000 \
    "$fixture_image" admin-login <"$password_file"
}

expect_login_rejected() {
  local password_file=$1
  if admin_login "$password_file" >"$run_dir/rejected.out" 2>"$run_dir/rejected.err"; then
    printf 'obsolete Casdoor recovery password still authenticates\n' >&2
    return 1
  fi
  grep -Fq 'admin login was rejected' "$run_dir/rejected.err"
  ! grep -Fq -f "$password_file" "$run_dir/rejected.err"
}

store_digest() {
  sha256sum "$workspace/.anas/secrets.yml" | awk '{print $1}'
}

active_deployment() {
  "$anas_cmd" status -w "$workspace" --json | jq -er '.active_deployment'
}

command -v "$anas_cmd" >/dev/null
test -x "$fixture_bin"
test -x "$wrapper"
test "$($real_docker inspect --format '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$casdoor")" = running\|healthy
issuer=$($real_docker exec "$casdoor" printenv CASDOOR_DOMAIN_FULL)
fixture_image=$($real_docker inspect --format '{{.Config.Image}}' "$casdoor")
install -d -m 0700 "$bin_dir"
ln -s "$wrapper" "$bin_dir/docker"

credential >"$run_dir/before.json"
extract_credential "$run_dir/before.json" "$run_dir/old.password"
admin_login "$run_dir/old.password" >"$run_dir/login-before.json"
jq -e '.login == "accepted" and .username == "admin_casdoor" and .is_admin == true' \
  "$run_dir/login-before.json" >/dev/null
printf 'break_glass_login=pass username=admin_casdoor source=managed_inventory\n'

env_file="$workspace/.anas/deployments/$(active_deployment)/modules/casdoor/.env"
! grep -Eq '^CASDOOR_(LOCAL_ADMIN__BREAK_GLASS_PASSWORD|BREAK_GLASS_PASSWORD)=' "$env_file"

"$anas_cmd" admin local rotate casdoor break_glass -w "$workspace" --json \
  >"$run_dir/rotate-success.json"
jq -e '.ok == true and .rotated == true and .account.username == "admin_casdoor"' \
  "$run_dir/rotate-success.json" >/dev/null
credential >"$run_dir/after.json"
extract_credential "$run_dir/after.json" "$run_dir/current.password"
! cmp -s "$run_dir/old.password" "$run_dir/current.password"
expect_login_rejected "$run_dir/old.password"
admin_login "$run_dir/current.password" >"$run_dir/login-after.json"
printf 'break_glass_rotation=pass new_password=accepted old_password=rejected\n'

before_failure_store=$(store_digest)
before_failure_deployment=$(active_deployment)
if PATH="$bin_dir:$PATH" \
  ANAS_TEST_REAL_DOCKER="$real_docker" \
  ANAS_TEST_FAIL_ONCE_MARKER="$marker" \
  ANAS_TEST_FAIL_CONTAINER="$casdoor" \
  "$anas_cmd" admin local rotate casdoor break_glass -w "$workspace" --json \
    >"$run_dir/rotate-failure.json" 2>"$run_dir/rotate-failure.err"; then
  failure_status=0
else
  failure_status=$?
fi
test "$failure_status" = 1
test -f "$marker"
jq -e '.ok == false and .error.code == "local_admin_rotate_failed"' \
  "$run_dir/rotate-failure.json" >/dev/null
test "$(store_digest)" = "$before_failure_store"
test "$(active_deployment)" = "$before_failure_deployment"
credential >"$run_dir/after-failure.json"
extract_credential "$run_dir/after-failure.json" "$run_dir/after-failure.password"
cmp -s "$run_dir/current.password" "$run_dir/after-failure.password"
admin_login "$run_dir/after-failure.password" >"$run_dir/login-after-failure.json"
expect_login_rejected "$run_dir/old.password"
printf 'break_glass_rollback=pass candidate_applied=true injected_failure=true previous_restored=true store_unchanged=true\n'

printf '\nCasdoor local-admin E2E tests passed\n'
