#!/usr/bin/env bash
# Casdoor OIDC user/admin logout and session-isolation E2E.
set -Eeuo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
export ANAS_TEST_CONTAINER_PREFIX=${ANAS_TEST_CONTAINER_PREFIX:-anas_casdoor_}
# shellcheck source=server-iam-matrix-common.sh
source "$script_dir/server-iam-matrix-common.sh"

casdoor="${prefix}casdoor"
dirwatch="${prefix}casdoor_dirwatch"
consumer="${prefix}nextcloud"
fixture_container="${prefix}casdoor_oidc_logout_consumer"
fixture_bin=${CASDOOR_LOGOUT_FIXTURE_BIN:-/home/whl/anas-casdoor-m3-e2e/casdoor-oidc-logout-consumer}
protocol_timeout=${CASDOOR_PROTOCOL_E2E_TIMEOUT:-420}

workdir=$(mktemp -d)
chmod 0700 "$workdir"
configured=false

section() { printf '\n== %s ==\n' "$1"; }

admin_fixture() {
  "$docker_cmd" exec "$casdoor" sh -c 'cat /run/secrets/casdoor-break-glass-password' |
    "$docker_cmd" exec -i "$fixture_container" /fixture "$@"
}

cleanup() {
  if [ "$configured" = true ] && "$docker_cmd" inspect "$fixture_container" >/dev/null 2>&1; then
    admin_fixture restore --backup /state/application-original.json >/dev/null 2>&1 || true
  fi
  "$docker_cmd" rm -f "$fixture_container" >/dev/null 2>&1 || true
  cleanup_matrix_users
  rm -rf "$workdir"
}
failure() {
  printf "FAIL: Casdoor OIDC logout E2E line=%s\n" "$1" >&2
  "$docker_cmd" logs --tail 100 "$fixture_container" >&2 || true
}
trap cleanup EXIT HUP INT TERM
trap 'failure "$LINENO"' ERR

casdoor_user() {
  "$docker_cmd" exec "$dirwatch" /opt/anas/bin/casdoor-helper directory-watch \
    --get-user "anas/$1" 2>/dev/null || printf 'null\n'
}

wait_for_user_state() {
  local user=$1 expression=$2 deadline current
  deadline=$(( $(date +%s) + protocol_timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    current=$(casdoor_user "$user")
    if printf '%s' "$current" | jq -e "$expression" >/dev/null 2>&1; then
      return 0
    fi
    sleep 5
  done
  printf 'Casdoor did not converge user %s; last state: %s\n' "$user" "$current" >&2
  return 1
}

fixture() {
  "$docker_cmd" exec "$fixture_container" /fixture "$@"
}

wait_probe() {
  local state_file=$1 expected=$2 attempt
  for attempt in $(seq 1 60); do
    if fixture probe --state-file "$state_file" --expect "$expected" >/dev/null 2>&1; then
      fixture probe --state-file "$state_file" --expect "$expected"
      return 0
    fi
    sleep 1
  done
  fixture probe --state-file "$state_file" --expect "$expected"
}

section "preflight"
for container in "$dc" "$casdoor" "$dirwatch" "$consumer"; do
  test "$("$docker_cmd" inspect --format '{{.State.Status}}' "$container")" = running
done
test -x "$fixture_bin"
command -v jq >/dev/null
test "$("$docker_cmd" exec "$casdoor" printenv ANAS_IAM_CLIENT__NEXTCLOUD__INTERFACE)" = oidc
issuer=$("$docker_cmd" exec "$casdoor" printenv CASDOOR_DOMAIN_FULL)
client_id=$("$docker_cmd" exec "$casdoor" printenv ANAS_IAM_CLIENT__NEXTCLOUD__CLIENT_ID)
managed_redirect_uris=$("$docker_cmd" exec "$casdoor" printenv ANAS_IAM_CLIENT__NEXTCLOUD__REDIRECT_URIS)
managed_backchannel_uri=$("$docker_cmd" exec "$casdoor" printenv ANAS_IAM_CLIENT__NEXTCLOUD__OIDC_LOGOUT_URI)
"$docker_cmd" exec "$consumer" printenv NEXTCLOUD_OIDC_CLIENT_SECRET >"$workdir/client-secret"
printf '%s\n' "$matrix_password" >"$workdir/user-password"
chmod 0600 "$workdir/client-secret" "$workdir/user-password"
fixture_image=$("$docker_cmd" inspect --format '{{.Config.Image}}' "$casdoor")
"$docker_cmd" rm -f "$fixture_container" >/dev/null 2>&1 || true
"$docker_cmd" run -d --name "$fixture_container" \
  --network "container:$casdoor" \
  --user "$(id -u):$(id -g)" \
  --entrypoint /fixture \
  -v "$fixture_bin:/fixture:ro" \
  -v "$workdir:/state" \
  -e "CASDOOR_FIXTURE_ISSUER=$issuer" \
  -e CASDOOR_FIXTURE_INTERNAL_ORIGIN=http://127.0.0.1:8000 \
  -e "CASDOOR_FIXTURE_CLIENT_ID=$client_id" \
  -e CASDOOR_FIXTURE_CLIENT_SECRET_FILE=/state/client-secret \
  -e CASDOOR_FIXTURE_REDIRECT_URI=http://127.0.0.1:18081/callback \
  -e CASDOOR_FIXTURE_BACKCHANNEL_URI=http://127.0.0.1:18081/backchannel \
  -e "CASDOOR_FIXTURE_MANAGED_REDIRECT_URIS=$managed_redirect_uris" \
  -e "CASDOOR_FIXTURE_MANAGED_BACKCHANNEL_URI=$managed_backchannel_uri" \
  "$fixture_image" serve >/dev/null
for attempt in $(seq 1 30); do
  fixture probe --state-file /state/not-created --expect active >/dev/null 2>&1 || true
  if "$docker_cmd" exec "$fixture_container" /fixture evidence --state-file /state/not-created >/dev/null 2>&1; then
    break
  fi
  if "$docker_cmd" exec "$fixture_container" sh -c 'wget -q -O /dev/null http://127.0.0.1:18081/healthz' >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
"$docker_cmd" exec "$fixture_container" sh -c 'wget -q -O /dev/null http://127.0.0.1:18081/healthz'
"$docker_cmd" exec "$casdoor" sh -c 'wget -q -O /dev/null http://127.0.0.1:18081/healthz'
configured=true
admin_fixture configure --backup /state/application-original.json
printf 'logout Consumer configured with exact callback and back-channel endpoints\n'

section "create directory-backed users"
create_matrix_users
for user in "$direct_user" "$all_user"; do
  wait_for_user_state "$user" \
    '.name == "'"$user"'" and .isForbidden == false and .isDeleted == false and (.id | length) > 0 and (.externalId | length) > 0'
done
wait_for_user_state "$direct_user" '(.groups | index("anas/APP_nextcloud")) != null'
wait_for_user_state "$all_user" '(.groups | index("anas/APP_all")) != null'

section "establish isolated application sessions"
fixture login --username "$direct_user" --password-file /state/user-password --state-file /state/same-user-1.json
fixture login --username "$direct_user" --password-file /state/user-password --state-file /state/same-user-2.json
fixture login --username "$all_user" --password-file /state/user-password --state-file /state/other-user.json
wait_probe /state/same-user-1.json active
wait_probe /state/same-user-2.json active
wait_probe /state/other-user.json active
sid_one=$(jq -er '.sid' "$workdir/same-user-1.json")
sid_two=$(jq -er '.sid' "$workdir/same-user-2.json")
test "$sid_one" != "$sid_two"
printf 'same_user_sessions=2 distinct_sid=true other_user_session=active\n'

section "user normal logout propagates to the application"
fixture user-logout --state-file /state/other-user.json
wait_probe /state/other-user.json revoked
wait_probe /state/same-user-1.json active
wait_probe /state/same-user-2.json active
fixture evidence --state-file /state/other-user.json
fixture replay
wait_probe /state/same-user-1.json active
printf 'user_logout=revoked_original_cookie unrelated_sessions=unchanged\n'

section "administrator deletes one exact Casdoor session"
admin_fixture admin-delete --state-file /state/same-user-2.json
wait_probe /state/same-user-2.json revoked
wait_probe /state/same-user-1.json active
wait_probe /state/other-user.json revoked
fixture evidence --state-file /state/same-user-2.json
fixture replay
wait_probe /state/same-user-1.json active
printf 'admin_delete=revoked_target_only same_user_peer=active other_user=unchanged\n'

section "restore managed Consumer configuration"
admin_fixture restore --backup /state/application-original.json
configured=false
printf 'consumer_configuration=restored redirect_and_backchannel=verified\n'

printf '\nCasdoor OIDC logout E2E tests passed\n'
