#!/usr/bin/env bash
# Casdoor signing-key overlap and managed OAuth client-secret rotation E2E.
set -Eeuo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
export ANAS_TEST_CONTAINER_PREFIX=${ANAS_TEST_CONTAINER_PREFIX:-anas_casdoor_}
# shellcheck source=server-iam-matrix-common.sh
source "$script_dir/server-iam-matrix-common.sh"

workspace=${ANAS_TEST_WORKSPACE:?ANAS_TEST_WORKSPACE is required}
anas_cmd=${ANAS_TEST_ANAS_CMD:-anas}
fixture_bin=${CASDOOR_LOGOUT_FIXTURE_BIN:-/home/whl/anas-casdoor-m3-e2e/casdoor-oidc-logout-consumer}
report_dir=${ANAS_TEST_REPORT_DIR:-$workspace/test-env/reports}
real_docker=${ANAS_TEST_REAL_DOCKER:-$(command -v docker)}
wrapper=${ANAS_TEST_DOCKER_WRAPPER:-$script_dir/server-casdoor-fail-once-docker.sh}
casdoor="${prefix}casdoor"
dirwatch="${prefix}casdoor_dirwatch"
timeout=${CASDOOR_PROTOCOL_E2E_TIMEOUT:-420}
test_suffix=${ANAS_TEST_MATRIX_SUFFIX:-$(date +%H%M%S)}
test_user="ick${test_suffix}"
test_password=${ANAS_TEST_MATRIX_PASSWORD:-Anas-Iam-${test_suffix}-E2e!}
run_root=$(mktemp -d)
bin_dir=$run_root/bin
marker=$run_root/fail-once.marker
project="${prefix}casdoor"

section() { printf '\n== %s ==\n' "$1"; }

workspace_active() {
  "$anas_cmd" status -w "$workspace" --json | jq -er '.active_deployment'
}

store_digest() {
  sha256sum "$workspace/.anas/secrets.yml" | awk '{print $1}'
}

wait_healthy() {
  local deadline state
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    state=$("$real_docker" inspect --format \
      '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$casdoor" 2>/dev/null || true)
    [ "$state" = 'running|healthy' ] && return 0
    sleep 5
  done
  printf 'Casdoor did not become healthy; last state=%s\n' "$state" >&2
  return 1
}

casdoor_user() {
  "$real_docker" exec "$dirwatch" /opt/anas/bin/casdoor-helper directory-watch \
    --get-user "anas/$test_user" 2>/dev/null || printf 'null\n'
}

wait_for_user() {
  local deadline current
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    current=$(casdoor_user)
    if printf '%s' "$current" | jq -e \
      '.name == "'"$test_user"'" and .isForbidden == false and .isDeleted == false and
       (.externalId | length) > 0 and (.groups | index("anas/Admins")) != null' >/dev/null 2>&1; then
      printf '%s\n' "$current"
      return 0
    fi
    sleep 5
  done
  printf 'Casdoor did not converge key-rotation user %s; last state: %s\n' "$test_user" "$current" >&2
  return 1
}

refresh_oidc_material() {
  issuer=$("$real_docker" exec "$casdoor" printenv CASDOOR_DOMAIN_FULL)
  oidc_client_id=$("$real_docker" exec "$casdoor" printenv ANAS_IAM_CLIENT__OAUTH2_PROXY__CLIENT_ID)
  oidc_redirect_uri=$("$real_docker" exec "$casdoor" printenv ANAS_IAM_CLIENT__OAUTH2_PROXY__REDIRECT_URIS |
    awk -F, '{print $1}')
  "$real_docker" exec "$casdoor" printenv ANAS_IAM_CLIENT__OAUTH2_PROXY__CLIENT_SECRET \
    >"$run_root/oidc-client-secret"
  chmod 0600 "$run_root/oidc-client-secret"
  fixture_image=$("$real_docker" inspect --format '{{.Config.Image}}' "$casdoor")
}

fixture_token_login() {
  local application=$1 organization=$2 client_id=$3 client_secret_file=$4
  local redirect_uri=$5 username=$6 password_file=$7 output_file=$8 token_file=${9:-}
  local -a token_args=()
  if [ -n "$token_file" ]; then
    token_args=(--token-file "/state/$token_file")
  fi
  "$real_docker" run --rm --network "container:$casdoor" \
    --entrypoint /fixture \
    -v "$fixture_bin:/fixture:ro" \
    -v "$run_root:/state" \
    -e "CASDOOR_FIXTURE_ISSUER=$issuer" \
    -e CASDOOR_FIXTURE_INTERNAL_ORIGIN=http://127.0.0.1:8000 \
    -e "CASDOOR_FIXTURE_APPLICATION=$application" \
    -e "CASDOOR_FIXTURE_ORGANIZATION=$organization" \
    -e "CASDOOR_FIXTURE_CLIENT_ID=$client_id" \
    -e "CASDOOR_FIXTURE_CLIENT_SECRET_FILE=/state/$client_secret_file" \
    -e "CASDOOR_FIXTURE_REDIRECT_URI=$redirect_uri" \
    "$fixture_image" token-login --username "$username" \
      --password-file "/state/$password_file" "${token_args[@]}" >"$run_root/$output_file"
}

verify_saved_token() {
  local token_file=$1 output_file=$2
  "$real_docker" run --rm --network "container:$casdoor" \
    --entrypoint /fixture \
    -v "$fixture_bin:/fixture:ro" \
    -v "$run_root:/state" \
    -e "CASDOOR_FIXTURE_ISSUER=$issuer" \
    -e CASDOOR_FIXTURE_INTERNAL_ORIGIN=http://127.0.0.1:8000 \
    -e CASDOOR_FIXTURE_APPLICATION=app-anas-oauth2-proxy \
    -e CASDOOR_FIXTURE_ORGANIZATION=anas \
    -e "CASDOOR_FIXTURE_CLIENT_ID=$oidc_client_id" \
    -e CASDOOR_FIXTURE_CLIENT_SECRET_FILE=/state/oidc-client-secret \
    -e "CASDOOR_FIXTURE_REDIRECT_URI=$oidc_redirect_uri" \
    "$fixture_image" verify-token --token-file "/state/$token_file" >"$run_root/$output_file"
}

portal_login() {
  local secret_file=$1 output_file=$2
  fixture_token_login app-built-in built-in "$portal_client_id" "$secret_file" \
    "$issuer/callback" admin_casdoor admin-password "$output_file"
}

expect_portal_secret_rejected() {
  local secret_file=$1
  if portal_login "$secret_file" rejected-portal-login.json \
    >"$run_root/rejected-portal.stdout" 2>"$run_root/rejected-portal.stderr"; then
    printf 'obsolete Casdoor portal client secret still authenticates\n' >&2
    return 1
  fi
  grep -Fq 'authorization code exchange failed' "$run_root/rejected-portal.stderr"
  ! grep -Fq -f "$run_root/$secret_file" "$run_root/rejected-portal.stderr"
}

cleanup_user() {
  samba_tool user delete "$test_user" >/dev/null 2>&1 || true
}

cleanup() {
  local status=$?
  set +e
  cleanup_user
  rm -rf -- "$run_root"
  if [ "$status" -ne 0 ]; then
    printf 'FAIL: Casdoor key-rotation E2E\n' >&2
  fi
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

section "preflight and real OIDC identity seed"
test -x "$fixture_bin"
test -x "$wrapper"
command -v jq >/dev/null
install -d -m 0700 "$bin_dir" "$report_dir"
ln -s "$wrapper" "$bin_dir/docker"
printf '%s\n' "$test_password" >"$run_root/user-password"
"$anas_cmd" admin local credential casdoor break_glass -w "$workspace" --json |
  jq -er '.account.password' >"$run_root/admin-password"
chmod 0600 "$run_root/user-password" "$run_root/admin-password"
wait_healthy
cleanup_user
samba_tool user add "$test_user" "$test_password" --userou='OU=People' \
  --mail-address="${test_user}@m5.nas.test" >/dev/null
samba_tool user setexpiry "$test_user" --noexpiry >/dev/null
samba_tool user rename "$test_user" --display-name="Casdoor key rotation E2E $test_user" >/dev/null
samba_tool group addmembers Admins "$test_user" >/dev/null
wait_anchor "$test_user"
wait_for_user >"$run_root/source-user.json"
source_sub=$(jq -er '.id' "$run_root/source-user.json")
refresh_oidc_material
fixture_token_login app-anas-oauth2-proxy anas "$oidc_client_id" oidc-client-secret \
  "$oidc_redirect_uri" "$test_user" user-password before-signing.json old-id-token
old_kid=$(jq -er '.kid' "$run_root/before-signing.json")
test "$(jq -er '.sub' "$run_root/before-signing.json")" = "$source_sub"
"$real_docker" exec "$casdoor" printenv CASDOOR_SIGNING_CERT >"$run_root/old-signing-cert"
chmod 0600 "$run_root/old-signing-cert" "$run_root/old-id-token"

section "signing-key transaction and trust overlap"
before_signing_deployment=$(workspace_active)
before_signing_store=$(store_digest)
"$anas_cmd" credential rotate casdoor.signing_key -w "$workspace" -y --json \
  >"$report_dir/casdoor-signing-rotation.json" \
  2>"$report_dir/casdoor-signing-rotation.log"
chmod 0600 "$report_dir/casdoor-signing-rotation.json" "$report_dir/casdoor-signing-rotation.log"
jq -e '.ok == true and .rotation.status == "complete" and
  (.rotation.credentials | index("casdoor.signing_key") != null)' \
  "$report_dir/casdoor-signing-rotation.json" >/dev/null
test "$(workspace_active)" != "$before_signing_deployment"
test "$(store_digest)" != "$before_signing_store"
wait_healthy
refresh_oidc_material
fixture_token_login app-anas-oauth2-proxy anas "$oidc_client_id" oidc-client-secret \
  "$oidc_redirect_uri" "$test_user" user-password after-signing.json new-id-token
new_kid=$(jq -er '.kid' "$run_root/after-signing.json")
test "$new_kid" != "$old_kid"
test "$(jq -er '.sub' "$run_root/after-signing.json")" = "$source_sub"
verify_saved_token old-id-token overlap-verify.json
jq -e --arg kid "$old_kid" '.verification == "accepted" and .kid == $kid' \
  "$run_root/overlap-verify.json" >/dev/null
"$real_docker" exec "$casdoor" printenv CASDOOR_SIGNING_CERT >"$run_root/new-signing-cert"
! cmp -s "$run_root/old-signing-cert" "$run_root/new-signing-cert"
active_env="$workspace/.anas/deployments/$(workspace_active)/modules/casdoor/.env"
grep -q '^CASDOOR_SIGNING_CERT=' "$active_env"
! grep -Fq 'PRIVATE KEY' "$workspace/.anas/deployments/$(workspace_active)/deployment.yml"
printf 'signing_rotation=pass old_kid=%s new_kid=%s new_login=true old_token_overlap=true public_projection_changed=true\n' \
  "$old_kid" "$new_kid"

section "managed portal OAuth client credential rotation"
portal_client_id=$("$real_docker" exec "$casdoor" printenv CASDOOR_PORTAL_CLIENT_ID)
"$real_docker" exec "$casdoor" printenv CASDOOR_PORTAL_CLIENT_SECRET >"$run_root/old-portal-secret"
chmod 0600 "$run_root/old-portal-secret"
portal_login old-portal-secret portal-before.json
before_portal_deployment=$(workspace_active)
"$anas_cmd" credential rotate casdoor.portal_client_secret -w "$workspace" -y --json \
  >"$report_dir/casdoor-portal-secret-rotation.json" \
  2>"$report_dir/casdoor-portal-secret-rotation.log"
chmod 0600 "$report_dir/casdoor-portal-secret-rotation.json" "$report_dir/casdoor-portal-secret-rotation.log"
jq -e '.ok == true and .rotation.status == "complete" and
  (.rotation.credentials | index("casdoor.portal_client_secret") != null)' \
  "$report_dir/casdoor-portal-secret-rotation.json" >/dev/null
test "$(workspace_active)" != "$before_portal_deployment"
wait_healthy
portal_client_id=$("$real_docker" exec "$casdoor" printenv CASDOOR_PORTAL_CLIENT_ID)
"$real_docker" exec "$casdoor" printenv CASDOOR_PORTAL_CLIENT_SECRET >"$run_root/current-portal-secret"
chmod 0600 "$run_root/current-portal-secret"
! cmp -s "$run_root/old-portal-secret" "$run_root/current-portal-secret"
portal_login current-portal-secret portal-after.json
expect_portal_secret_rejected old-portal-secret
printf 'client_credential_rotation=pass real_oauth_before=true new_secret=true old_secret=rejected\n'

section "candidate failure restores previous deployment and Store"
before_failure_deployment=$(workspace_active)
before_failure_store=$(store_digest)
before_failure_secret=$(sha256sum "$run_root/current-portal-secret" | awk '{print $1}')
if PATH="$bin_dir:$PATH" \
  ANAS_TEST_REAL_DOCKER="$real_docker" \
  ANAS_TEST_FAIL_ONCE_MARKER="$marker" \
  ANAS_TEST_FAIL_PROJECT="$project" \
  "$anas_cmd" credential rotate casdoor.portal_client_secret -w "$workspace" -y --json \
  >"$report_dir/casdoor-credential-rotation-failure.json" \
  2>"$report_dir/casdoor-credential-rotation-failure.log"; then
  failure_status=0
else
  failure_status=$?
fi
chmod 0600 "$report_dir/casdoor-credential-rotation-failure.json" \
  "$report_dir/casdoor-credential-rotation-failure.log"
test "$failure_status" = 1
test -f "$marker"
jq -e '.ok == false and .error.code == "credential_rotation_failed" and
  .error.detail.rotation.status == "previous_restored"' \
  "$report_dir/casdoor-credential-rotation-failure.json" >/dev/null
test "$(workspace_active)" = "$before_failure_deployment"
test "$(store_digest)" = "$before_failure_store"
wait_healthy
"$real_docker" exec "$casdoor" printenv CASDOOR_PORTAL_CLIENT_SECRET >"$run_root/after-failure-portal-secret"
test "$(sha256sum "$run_root/after-failure-portal-secret" | awk '{print $1}')" = "$before_failure_secret"
portal_login current-portal-secret portal-after-failure.json
verify_saved_token old-id-token overlap-after-failure.json
for secret_file in old-portal-secret current-portal-secret; do
  ! grep -Fq -f "$run_root/$secret_file" "$report_dir/casdoor-credential-rotation-failure.log"
  ! grep -Fq -f "$run_root/$secret_file" "$report_dir/casdoor-credential-rotation-failure.json"
done
printf 'rotation_failure_recovery=pass previous_restored=true store_unchanged=true live_credential_unchanged=true\n'

cleanup_user
printf '\nCasdoor key-rotation E2E tests passed\n'
