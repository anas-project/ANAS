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
  samba_tool user delete "irl${matrix_suffix}" >/dev/null 2>&1 || true
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
  for attempt in $(seq 1 120); do
    if fixture probe --state-file "$state_file" --expect "$expected" >/dev/null 2>&1; then
      fixture probe --state-file "$state_file" --expect "$expected"
      return 0
    fi
    sleep 1
  done
  fixture probe --state-file "$state_file" --expect "$expected"
}

wait_directory_state() {
  local path=$1 expression=$2 argument_name=${3:-sid} argument_value=${4:-} attempt
  for attempt in $(seq 1 120); do
    if "$docker_cmd" exec "$dirwatch" cat "/data/anas-dirwatch/$path" |
      jq -e --arg "$argument_name" "$argument_value" "$expression" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  printf 'Directory state did not converge: %s\n' "$path" >&2
  return 1
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
admin_fixture revocation-auth
printf 'logout Consumer configured with exact callback and back-channel endpoints\n'
if [ "${CASDOOR_LOGOUT_AUTH_ONLY:-0}" = 1 ]; then
  admin_fixture restore --backup /state/application-original.json
  configured=false
  printf 'directory_revocation_authorization=passed\n'
  exit 0
fi

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
if [ "${CASDOOR_DIRECTORY_LOGOUT_E2E:-0}" = 1 ]; then
  fixture refresh --state-file /state/other-user.json --expect rejected
fi
wait_probe /state/same-user-1.json active
wait_probe /state/same-user-2.json active
fixture evidence --state-file /state/other-user.json
fixture replay
wait_probe /state/same-user-1.json active
printf 'user_logout=revoked_original_cookie unrelated_sessions=unchanged\n'

section "administrator deletes one exact Casdoor session"
admin_fixture admin-delete --state-file /state/same-user-2.json
wait_probe /state/same-user-2.json revoked
if [ "${CASDOOR_DIRECTORY_LOGOUT_E2E:-0}" = 1 ]; then
  fixture refresh --state-file /state/same-user-2.json --expect rejected
  fixture refresh --state-file /state/same-user-1.json --expect accepted
fi
wait_probe /state/same-user-1.json active
wait_probe /state/other-user.json revoked
fixture evidence --state-file /state/same-user-2.json
fixture replay
wait_probe /state/same-user-1.json active
printf 'admin_delete=revoked_target_only same_user_peer=active other_user=unchanged\n'

if [ "${CASDOOR_DIRECTORY_LOGOUT_E2E:-0}" = 1 ]; then
  section "refresh and UserInfo keep the anchor subject and issued sid"
  fixture refresh --state-file /state/same-user-1.json --expect accepted
  test "$(jq -r '.sub' "$workdir/same-user-1.json")" = "$(casdoor_user "$direct_user" | jq -r '.externalId')"
  fixture login --username "$all_user" --password-file /state/user-password --state-file /state/unaffected.json

  section "group removal revokes existing cookies and refresh grants"
  samba_tool group removemembers APP_nextcloud "$direct_user" >/dev/null
  wait_for_user_state "$direct_user" '(.groups | index("anas/APP_nextcloud")) == null'
  wait_probe /state/same-user-1.json revoked
  fixture refresh --state-file /state/same-user-1.json --expect rejected
  wait_probe /state/unaffected.json active

  section "receiver failure persists intent across watcher restart and regrant"
  samba_tool group addmembers APP_nextcloud "$direct_user" >/dev/null
  wait_for_user_state "$direct_user" '(.groups | index("anas/APP_nextcloud")) != null'
  fixture login --username "$direct_user" --password-file /state/user-password --state-file /state/old-before-retry.json
  fixture receiver --fail
  samba_tool group removemembers APP_nextcloud "$direct_user" >/dev/null
  wait_for_user_state "$direct_user" '(.groups | index("anas/APP_nextcloud")) == null'
  fixture refresh --state-file /state/old-before-retry.json --expect rejected
  wait_probe /state/old-before-retry.json active
  # Profile publication precedes revoke acknowledgement and its durable write.
  # Wait for this grant, rather than accepting another user's pending record.
  wait_directory_state pending-logouts.json 'any(.[]; .revoked == true and any(.snapshot.targets[]?; .sid == $sid))' sid "$(jq -r '.sid' "$workdir/old-before-retry.json")"
  "$docker_cmd" restart "$dirwatch" >/dev/null
  samba_tool group addmembers APP_nextcloud "$direct_user" >/dev/null
  wait_for_user_state "$direct_user" '(.groups | index("anas/APP_nextcloud")) != null'
  fixture login --username "$direct_user" --password-file /state/user-password --state-file /state/new-after-regrant.json
  test "$(jq -r '.sid' "$workdir/old-before-retry.json")" != "$(jq -r '.sid' "$workdir/new-after-regrant.json")"
  fixture receiver --fail=false
  wait_probe /state/old-before-retry.json revoked
  wait_probe /state/new-after-regrant.json active
  wait_probe /state/unaffected.json active
  fixture refresh --state-file /state/new-after-regrant.json --expect accepted

  section "rename revokes old authorization while preserving identity"
  original_direct_user=$direct_user
  original_direct_id=$(casdoor_user "$direct_user" | jq -r '.id')
  original_direct_sub=$(jq -r '.sub' "$workdir/new-after-regrant.json")
  renamed_user="irl${matrix_suffix}"
  samba_tool user rename "$direct_user" --samaccountname="$renamed_user" >/dev/null
  wait_for_user_state "$renamed_user" '.name == "'"$renamed_user"'" and .isForbidden == false'
  wait_probe /state/new-after-regrant.json revoked
  fixture refresh --state-file /state/new-after-regrant.json --expect rejected
  test "$(casdoor_user "$renamed_user" | jq -r '.id')" = "$original_direct_id"
  fixture login --username "$renamed_user" --password-file /state/user-password --state-file /state/after-rename.json
  test "$(jq -r '.sub' "$workdir/after-rename.json")" = "$original_direct_sub"
  wait_probe /state/after-rename.json active
  wait_probe /state/unaffected.json active
  direct_user=$renamed_user

  section "directory disable terminates the existing application session"
  samba_tool user disable "$direct_user" >/dev/null
  wait_for_user_state "$direct_user" '.isForbidden == true'
  wait_probe /state/after-rename.json revoked
  fixture refresh --state-file /state/after-rename.json --expect rejected
  wait_probe /state/unaffected.json active

  section "recursive group removal terminates an existing application session"
  wait_for_user_state "$nested_user" '(.groups | index("anas/APP_nextcloud")) != null'
  fixture login --username "$nested_user" --password-file /state/user-password --state-file /state/nested.json
  samba_tool group removemembers APP_nextcloud "$nested_group" >/dev/null
  wait_for_user_state "$nested_user" '(.groups | index("anas/APP_nextcloud")) == null'
  wait_probe /state/nested.json revoked
  wait_probe /state/unaffected.json active

  section "directory delete terminates the existing application session"
  samba_tool user delete "$all_user" >/dev/null
  wait_for_user_state "$all_user" '.isForbidden == true and .isDeleted == true'
  wait_probe /state/unaffected.json revoked
  section "recycled username with a new anchor is quarantined"
  deleted_profile=$(casdoor_user "$all_user")
  old_anchor=$(printf '%s' "$deleted_profile" | jq -r '.externalId')
  old_id=$(printf '%s' "$deleted_profile" | jq -r '.id')
  samba_tool user add "$all_user" "$matrix_password" --userou='OU=People' --mail-address="${all_user}@${matrix_email_domain}" >/dev/null
  samba_tool group addmembers APP_all "$all_user" >/dev/null
  wait_anchor "$all_user"
  new_anchor=$(samba_tool user show "$all_user" --attributes=anasIdentityAnchor | sed -n 's/^anasIdentityAnchor: //p')
  test "$new_anchor" != "$old_anchor"
  for attempt in $(seq 1 90); do
    if "$docker_cmd" exec "$dirwatch" cat /data/anas-dirwatch/health.json | jq -e '.last_error | contains("directory identity conflict")' >/dev/null; then break; fi
    sleep 2
  done
  wait_directory_state health.json '.ready == false and (.last_error | contains("directory identity conflict"))'
  reused_profile=$(casdoor_user "$all_user")
  printf '%s' "$reused_profile" | jq -e --arg anchor "$old_anchor" --arg id "$old_id" '.externalId == $anchor and .id == $id and .isForbidden == true' >/dev/null
  if fixture login --username "$all_user" --password-file /state/user-password --state-file /state/reused.json >/dev/null 2>&1; then
    printf 'recycled username unexpectedly inherited an account\n' >&2
    exit 1
  fi
  samba_tool user delete "$all_user" >/dev/null
  printf 'directory_rename=passed recycled_label=quarantined old_identity=preserved\n'
  printf 'directory_logout=passed refresh_subject_sid=verified receiver_retry_restart_regrant=passed scope=affected_user\n'
fi

section "restore managed Consumer configuration"
admin_fixture restore --backup /state/application-original.json
configured=false
printf 'consumer_configuration=restored redirect_and_backchannel=verified\n'

printf '\nCasdoor OIDC logout E2E tests passed\n'
