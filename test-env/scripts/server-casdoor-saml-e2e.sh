#!/usr/bin/env bash
# Casdoor SAML SP-initiated login and application-policy E2E.
set -Eeuo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fixture_dir=$(cd -- "$script_dir/../fixtures/casdoor-saml-consumer" && pwd)
export ANAS_TEST_CONTAINER_PREFIX=${ANAS_TEST_CONTAINER_PREFIX:-anas_casdoor_}
# shellcheck source=server-iam-matrix-common.sh
source "$script_dir/server-iam-matrix-common.sh"

consumer="${prefix}nextcloud"
casdoor="${prefix}casdoor"
dirwatch="${prefix}casdoor_dirwatch"
protocol_timeout=${CASDOOR_PROTOCOL_E2E_TIMEOUT:-420}
renamed_user="isz${matrix_suffix}"

workdir=$(mktemp -d)
chmod 0700 "$workdir"
accounts="$workdir/accounts.json"
printf '{}\n' >"$accounts"
chmod 0600 "$accounts"

section() { printf '\n== %s ==\n' "$1"; }

cleanup() {
  samba_tool user delete "$renamed_user" >/dev/null 2>&1 || true
  cleanup_matrix_users
  rm -rf "$workdir"
}
trap cleanup EXIT HUP INT TERM
trap 'printf "FAIL: Casdoor SAML E2E line=%s\n" "$LINENO" >&2' ERR

container_env() {
  "$docker_cmd" exec "$1" printenv "$2"
}

consumer_curl() {
  "$docker_cmd" exec "$consumer" curl -skS --connect-timeout 10 --max-time 90 "$@"
}

casdoor_user() {
  "$docker_cmd" exec "$dirwatch" /opt/anas/bin/casdoor-helper directory-watch \
    --get-user "anas/$1" 2>/dev/null || printf 'null\n'
}

wait_for_user_state() {
  local user=$1 expression=$2 description=$3 deadline current
  deadline=$(( $(date +%s) + protocol_timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    current=$(casdoor_user "$user")
    if printf '%s' "$current" | jq -e "$expression" >/dev/null 2>&1; then
      return 0
    fi
    sleep 5
  done
  "$docker_cmd" logs --tail 100 "$dirwatch" >&2 || true
  printf 'Casdoor did not converge %s (%s); last state: %s\n' \
    "$user" "$description" "$current" >&2
  return 1
}

new_request() {
  "$saml_consumer" request --metadata "$metadata_file" --entity-id "$sp_entity_id" \
    --acs-url "$acs_url" --sso-url "$sso_url"
}

login_response() {
  local username=$1 saml_request=$2 relay_state=$3 payload
  payload=$(jq -cn \
    --arg application "$application" --arg username "$username" \
    --arg password "$matrix_password" --arg request "$saml_request" \
    --arg relay "$relay_state" \
    '{application:$application,organization:"anas",username:$username,password:$password,
      autoSignin:false,type:"saml",signinMethod:"Password",samlRequest:$request,relayState:$relay}')
  printf '%s' "$payload" | "$docker_cmd" exec -i "$consumer" \
    curl -skS --connect-timeout 10 --max-time 90 \
      -H 'Content-Type: application/json' --data-binary @- "$issuer/api/login"
}

expect_login_denied() {
  local username=$1 reason=$2 request response
  request=$(new_request)
  response=$(login_response "$username" "$(printf '%s' "$request" | jq -er '.request')" \
    "denied-$matrix_suffix-$username")
  printf '%s' "$response" | jq -e \
    '.status == "error" and ((.data // "") | length) == 0' >/dev/null
  printf 'saml_login=denied username=%s reason=%s\n' "$username" "$reason"
}

materialize_account() {
  local assertion_file=$1 permission=$2
  python3 - "$accounts" "$assertion_file" "$anchor_claim" "$permission" <<'PY'
import json
import pathlib
import sys

accounts_path, assertion_path, anchor_claim, permission = sys.argv[1:]
accounts_file = pathlib.Path(accounts_path)
accounts = json.loads(accounts_file.read_text())
assertion = json.loads(pathlib.Path(assertion_path).read_text())
attributes = assertion["attributes"]
anchor = attributes[anchor_claim][0]
accounts[anchor] = {
    "anchor": anchor,
    "name_id": assertion["name_id"],
    "username": attributes["preferred_username"][0],
    "display_name": attributes["name"][0],
    "email": attributes["email"][0],
    "permission": permission,
}
accounts_file.write_text(json.dumps(accounts, sort_keys=True))
PY
}

login_allowed() {
  local username=$1 expected_group=$2 expected_permission=$3 expected_anchor=$4
  local profile_username=${5:-$username}
  local request request_id encoded response response_file assertion_file relay_state
  request=$(new_request)
  request_id=$(printf '%s' "$request" | jq -er '.id')
  encoded=$(printf '%s' "$request" | jq -er '.request')
  relay_state="relay-$matrix_suffix-$username"
  response=$(login_response "$username" "$encoded" "$relay_state")
  if ! printf '%s' "$response" | jq -e \
    --arg acs "$acs_url" \
    '.status == "ok" and (.data | length) > 0 and .data2.redirectUrl == $acs and
      ((.data2.method | ascii_downcase) == "post")' >/dev/null; then
    printf '%s' "$response" | jq \
      '{status,msg,data2,data_length: ((.data // "") | length)}' >&2
    return 1
  fi
  response_file="$workdir/response-$username.b64"
  assertion_file="$workdir/assertion-$username.json"
  printf '%s' "$response" | jq -er '.data' >"$response_file"
  chmod 0600 "$response_file"
  "$saml_consumer" verify --metadata "$metadata_file" --entity-id "$sp_entity_id" \
    --acs-url "$acs_url" --response "$response_file" --request-id "$request_id" \
    --name-id "$username" --attribute "preferred_username=$username" \
    --attribute "name=IAM E2E $profile_username" \
    --attribute "email=$profile_username@$matrix_email_domain" \
    --attribute "$anchor_claim=$expected_anchor" --attribute "groups=$expected_group" \
    >"$assertion_file"
  chmod 0600 "$assertion_file"
  materialize_account "$assertion_file" "$expected_permission"
  rm -f "$response_file" "$assertion_file"
  printf 'saml_login=allowed username=%s group=%s permission=%s assertion_signature=verified\n' \
    "$username" "$expected_group" "$expected_permission"
}

section "preflight"
for container in "$dc" "$casdoor" "$dirwatch" "$consumer"; do
  test "$("$docker_cmd" inspect --format '{{.State.Status}}' "$container")" = running
done
command -v jq >/dev/null
if [ -z "${CASDOOR_SAML_CONSUMER_BIN:-}" ]; then
  command -v go >/dev/null
fi
container_env "$dirwatch" CASDOOR_DIRWATCH_MANAGED_GROUPS | grep -Eq '(^|,)APP_nextcloud(,|$)'
test "$(container_env "$casdoor" ANAS_IAM_CLIENT__NEXTCLOUD__INTERFACE)" = saml
issuer=$(container_env "$casdoor" CASDOOR_DOMAIN_FULL)
application=app-anas-nextcloud
sp_entity_id=$(container_env "$casdoor" ANAS_IAM_CLIENT__NEXTCLOUD__SP_ENTITY_ID)
acs_url=$(container_env "$casdoor" ANAS_IAM_CLIENT__NEXTCLOUD__ACS_URL)
metadata_url=$(container_env "$casdoor" ANAS_IAM_BINDING__NEXTCLOUD__SAML_METADATA_URL)
sso_url=$(container_env "$casdoor" ANAS_IAM_BINDING__NEXTCLOUD__SAML_SSO_URL)
anchor_claim=$(container_env "$casdoor" SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE)
metadata_file="$workdir/idp-metadata.xml"
consumer_curl "$metadata_url" >"$metadata_file"
chmod 0600 "$metadata_file"
saml_consumer=${CASDOOR_SAML_CONSUMER_BIN:-$workdir/casdoor-saml-consumer}
if [ -z "${CASDOOR_SAML_CONSUMER_BIN:-}" ]; then
  (cd "$fixture_dir" && go build -o "$saml_consumer" .)
fi
test -x "$saml_consumer"
new_request | jq -e '(.id | length) > 0 and (.request | length) > 0' >/dev/null
printf 'SAML metadata, SP registration, AuthnRequest generation, and signature verifier are ready\n'

section "create Samba account and authorization matrix"
create_matrix_users
for user in "$direct_user" "$all_user" "$admin_user" "$denied_user" "$nested_user"; do
  wait_for_user_state "$user" \
    '.name == "'"$user"'" and .isForbidden == false and .isDeleted == false and (.externalId | length) > 0' \
    'active directory shadow'
done
wait_for_user_state "$direct_user" '(.groups | index("anas/APP_nextcloud")) != null' 'direct group'
wait_for_user_state "$all_user" '(.groups | index("anas/APP_all")) != null' 'all-applications group'
wait_for_user_state "$admin_user" '(.groups | index("anas/Admins")) != null' 'administrator group'
wait_for_user_state "$nested_user" '(.groups | index("anas/APP_nextcloud")) != null' 'recursive group'
wait_for_user_state "$denied_user" '(.groups | length) == 0' 'no allowed group'
direct_anchor=$(casdoor_user "$direct_user" | jq -er '.externalId')
printf 'Samba authorization matrix converged in Casdoor\n'

section "signed assertions, attributes, and application permissions"
login_allowed "$direct_user" APP_nextcloud user "$direct_anchor"
all_anchor=$(casdoor_user "$all_user" | jq -er '.externalId')
login_allowed "$all_user" APP_all user "$all_anchor"
admin_anchor=$(casdoor_user "$admin_user" | jq -er '.externalId')
login_allowed "$admin_user" Admins app-admin "$admin_anchor"
nested_anchor=$(casdoor_user "$nested_user" | jq -er '.externalId')
login_allowed "$nested_user" APP_nextcloud user "$nested_anchor"
expect_login_denied "$denied_user" application-group-policy
expect_login_denied "$disabled_user" directory-authentication
test "$(jq 'length' "$accounts")" = 4

section "disable, group revocation, rename, and delete"
samba_tool user disable "$direct_user" >/dev/null
wait_for_user_state "$direct_user" '.isForbidden == true and (.groups | length) == 0' 'disabled'
expect_login_denied "$direct_user" disabled
samba_tool user enable "$direct_user" >/dev/null
wait_for_user_state "$direct_user" \
  '.isForbidden == false and (.groups | index("anas/APP_nextcloud")) != null' 're-enabled'
login_allowed "$direct_user" APP_nextcloud user "$direct_anchor"

samba_tool group removemembers APP_nextcloud "$direct_user" >/dev/null
wait_for_user_state "$direct_user" '(.groups | index("anas/APP_nextcloud")) == null' 'group removed'
expect_login_denied "$direct_user" application-group-policy
samba_tool group addmembers APP_nextcloud "$direct_user" >/dev/null
wait_for_user_state "$direct_user" '(.groups | index("anas/APP_nextcloud")) != null' 'group restored'

samba_tool group addmembers APP_nextcloud "$admin_user" >/dev/null
wait_for_user_state "$admin_user" \
  '(.groups | index("anas/APP_nextcloud")) != null and (.groups | index("anas/Admins")) != null' \
  'administrator also admitted as application user'
samba_tool group removemembers Admins "$admin_user" >/dev/null
wait_for_user_state "$admin_user" \
  '(.groups | index("anas/Admins")) == null and (.groups | index("anas/APP_nextcloud")) != null' \
  'administrator privilege removed while application access remains'
login_allowed "$admin_user" APP_nextcloud user "$admin_anchor"
test "$(jq -r --arg anchor "$admin_anchor" '.[$anchor].permission' "$accounts")" = user

samba_tool user rename "$direct_user" --samaccountname="$renamed_user" >/dev/null
wait_for_user_state "$renamed_user" \
  '.externalId == "'"$direct_anchor"'" and (.groups | index("anas/APP_nextcloud")) != null' 'renamed'
login_allowed "$renamed_user" APP_nextcloud user "$direct_anchor" "$direct_user"
expect_login_denied "$direct_user" old-directory-name
test "$(jq 'length' "$accounts")" = 4
test "$(jq -r --arg anchor "$direct_anchor" '.[$anchor].username' "$accounts")" = "$renamed_user"

samba_tool user delete "$renamed_user" >/dev/null
wait_for_user_state "$renamed_user" \
  '.isForbidden == true and .isDeleted == true and (.groups | length) == 0' 'deleted'
expect_login_denied "$renamed_user" deleted

printf '\nCasdoor SAML SP-initiated E2E tests passed\n'
