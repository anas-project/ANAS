#!/usr/bin/env bash
# Casdoor OIDC Authorization Code and application-policy E2E.
#
# The Nextcloud registration supplies a real confidential client. This test
# uses its running container as a protocol Consumer fixture so the client
# secret never leaves that scope or appears in argv. The fixture completes the
# code exchange, verifies the RS256 ID token, materializes an application
# account keyed by the Samba anchor, and maps Admins to application admin.
set -Eeuo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
export ANAS_TEST_CONTAINER_PREFIX=${ANAS_TEST_CONTAINER_PREFIX:-anas_casdoor_}
# shellcheck source=server-iam-matrix-common.sh
source "$script_dir/server-iam-matrix-common.sh"

consumer="${prefix}nextcloud"
casdoor="${prefix}casdoor"
dirwatch="${prefix}casdoor_dirwatch"
protocol_timeout=${CASDOOR_PROTOCOL_E2E_TIMEOUT:-420}
renamed_user="ioz${matrix_suffix}"

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
trap 'printf "FAIL: Casdoor OIDC E2E line=%s\n" "$LINENO" >&2' ERR

container_env() {
  "$docker_cmd" exec "$1" printenv "$2"
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

urlencode() {
  jq -nr --arg value "$1" '$value | @uri'
}

consumer_curl() {
  "$docker_cmd" exec "$consumer" curl -skS --connect-timeout 10 --max-time 90 "$@"
}

login_response() {
  local username=$1 nonce=$2 state=$3 payload url
  payload=$(jq -cn \
    --arg application "$application" \
    --arg username "$username" \
    --arg password "$matrix_password" \
    '{application:$application,organization:"anas",username:$username,password:$password,
      autoSignin:false,type:"code",signinMethod:"Password"}')
  url="$issuer/api/login?clientId=$(urlencode "$client_id")&responseType=code"
  url="$url&redirectUri=$(urlencode "$redirect_uri")&scope=$(urlencode 'openid email profile')"
  url="$url&state=$(urlencode "$state")&nonce=$(urlencode "$nonce")"
  printf '%s' "$payload" | "$docker_cmd" exec -i "$consumer" \
    curl -skS --connect-timeout 10 --max-time 90 \
      -H 'Content-Type: application/json' --data-binary @- "$url"
}

expect_login_denied() {
  local username=$1 reason=$2 response
  response=$(login_response "$username" "denied-$matrix_suffix" "denied-$matrix_suffix")
  printf '%s' "$response" | jq -e \
    '.status == "error" and ((.data // "") | length) == 0' >/dev/null
  printf 'oidc_login=denied username=%s reason=%s\n' "$username" "$reason"
}

exchange_code() {
  local code=$1 output=$2
  # Read the one-time code on stdin; the Consumer obtains its client secret
  # from its existing environment and never prints either credential.
  printf '%s\n' "$code" | "$docker_cmd" exec -i "$consumer" sh -lc '
    IFS= read -r authorization_code
    exec curl -skS --connect-timeout 10 --max-time 90 \
      -X POST -H "Content-Type: application/x-www-form-urlencoded" \
      --data-urlencode grant_type=authorization_code \
      --data-urlencode client_id="$NEXTCLOUD_OIDC_CLIENT_ID" \
      --data-urlencode client_secret="$NEXTCLOUD_OIDC_CLIENT_SECRET" \
      --data-urlencode code="$authorization_code" \
      --data-urlencode redirect_uri="$NEXTCLOUD_DOMAIN_FULL/apps/user_oidc/code" \
      "$NEXTCLOUD_OIDC_ISSUER_URL/api/login/oauth/access_token"
  ' >"$output"
  chmod 0600 "$output"
  jq -e \
    '(.error // "") == "" and (.access_token | length) > 0 and (.id_token | length) > 0' \
    "$output" >/dev/null
}

verify_jwt() {
  local token_file=$1 nonce=$2 username=$3 expected_group=$4 expected_permission=$5
  local expected_sub=$6 expected_anchor=$7 claims_file=$workdir/claims.json
  local profile_username=${8:-$username}
  local signed_file=$workdir/jwt.signed signature_file=$workdir/jwt.signature
  local public_key_file=$workdir/jwt-public.pem jwks_file=$workdir/jwks.json

  consumer_curl "$jwks_uri" >"$jwks_file"
  chmod 0600 "$jwks_file"
  python3 - "$token_file" "$jwks_file" "$claims_file" "$signed_file" \
    "$signature_file" "$public_key_file" <<'PY'
import base64
import json
import pathlib
import sys

token_path, jwks_path, claims_path, signed_path, signature_path, key_path = sys.argv[1:]
token = json.loads(pathlib.Path(token_path).read_text())["id_token"]
parts = token.split(".")
if len(parts) != 3:
    raise SystemExit("ID token is not a compact JWT")

def b64url(value):
    return base64.urlsafe_b64decode(value + "=" * (-len(value) % 4))

header = json.loads(b64url(parts[0]))
claims = json.loads(b64url(parts[1]))
if header.get("alg") != "RS256" or not header.get("kid"):
    raise SystemExit("ID token is not an identified RS256 JWT")
jwks = json.loads(pathlib.Path(jwks_path).read_text())
matches = [item for item in jwks.get("keys", []) if item.get("kid") == header["kid"] and item.get("kty") == "RSA"]
if len(matches) != 1:
    raise SystemExit("JWT kid did not resolve to exactly one RSA JWK")
jwk = matches[0]

def der_length(length):
    if length < 128:
        return bytes([length])
    raw = length.to_bytes((length.bit_length() + 7) // 8, "big")
    return bytes([0x80 | len(raw)]) + raw

def der(tag, value):
    return bytes([tag]) + der_length(len(value)) + value

def integer(value):
    raw = value.to_bytes((value.bit_length() + 7) // 8, "big") or b"\x00"
    if raw[0] & 0x80:
        raw = b"\x00" + raw
    return der(0x02, raw)

n = int.from_bytes(b64url(jwk["n"]), "big")
e = int.from_bytes(b64url(jwk["e"]), "big")
rsa_key = der(0x30, integer(n) + integer(e))
rsa_algorithm = der(0x30, bytes.fromhex("06092a864886f70d010101") + der(0x05, b""))
subject_public_key = der(0x30, rsa_algorithm + der(0x03, b"\x00" + rsa_key))
pem = base64.encodebytes(subject_public_key).decode().replace("\n", "")
pem = "-----BEGIN PUBLIC KEY-----\n" + "\n".join(pem[i:i+64] for i in range(0, len(pem), 64)) + "\n-----END PUBLIC KEY-----\n"

pathlib.Path(claims_path).write_text(json.dumps(claims, sort_keys=True))
pathlib.Path(signed_path).write_bytes((parts[0] + "." + parts[1]).encode())
pathlib.Path(signature_path).write_bytes(b64url(parts[2]))
pathlib.Path(key_path).write_text(pem)
PY
  openssl dgst -sha256 -verify "$public_key_file" -signature "$signature_file" \
    "$signed_file" >/dev/null

  if ! jq -e \
    --arg issuer "$issuer" --arg client "$client_id" --arg nonce "$nonce" \
    --arg username "$username" --arg display "IAM E2E $profile_username" \
    --arg email "$profile_username@$matrix_email_domain" --arg group "$expected_group" \
    --arg anchor_key "$anchor_claim" --arg anchor "$expected_anchor" \
    --arg sub "$expected_sub" '
      .iss == $issuer and (.aud | index($client) != null) and .nonce == $nonce and
      (.sid | type) == "string" and (.sid | length) > 0 and
      .sub == $sub and .preferred_username == $username and .name == $display and
      .displayName == $display and .email == $email and .[$anchor_key] == $anchor and
      (.groups | type) == "array" and (.groups | index($group) != null) and
      (.exp | type) == "number" and .exp > now and (.iat | type) == "number" and .iat <= now
    ' "$claims_file" >/dev/null; then
    jq --arg anchor_key "$anchor_claim" \
      '{iss,aud,nonce,sid,sub,preferred_username,name,displayName,email,groups,
        exp,iat,anchor: .[$anchor_key]}' "$claims_file" >&2
    return 1
  fi

  python3 - "$accounts" "$claims_file" "$anchor_claim" "$expected_permission" <<'PY'
import json
import pathlib
import sys

accounts_path, claims_path, anchor_claim, permission = sys.argv[1:]
accounts_file = pathlib.Path(accounts_path)
accounts = json.loads(accounts_file.read_text())
claims = json.loads(pathlib.Path(claims_path).read_text())
anchor = claims[anchor_claim]
record = {
    "anchor": anchor,
    "subject": claims["sub"],
    "username": claims["preferred_username"],
    "display_name": claims["displayName"],
    "email": claims["email"],
    "permission": permission,
}
if anchor in accounts and accounts[anchor]["subject"] != record["subject"]:
    raise SystemExit("one permanent anchor resolved to a different immutable subject")
accounts[anchor] = record
accounts_file.write_text(json.dumps(accounts, sort_keys=True))
PY
}

login_allowed() {
  local username=$1 expected_group=$2 expected_permission=$3
  local expected_sub=$4 expected_anchor=$5 nonce state response code token_file
  local profile_username=${6:-$username}
  nonce="nonce-$matrix_suffix-$username"
  state="state-$matrix_suffix-$username"
  response=$(login_response "$username" "$nonce" "$state")
  printf '%s' "$response" | jq -e '.status == "ok" and (.data | length) > 0' >/dev/null
  code=$(printf '%s' "$response" | jq -er '.data')
  token_file="$workdir/token-$username.json"
  exchange_code "$code" "$token_file"
  verify_jwt "$token_file" "$nonce" "$username" "$expected_group" \
    "$expected_permission" "$expected_sub" "$expected_anchor" "$profile_username"
  rm -f "$token_file" "$workdir/claims.json" "$workdir/jwt.signed" \
    "$workdir/jwt.signature" "$workdir/jwt-public.pem" "$workdir/jwks.json"
  printf 'oidc_login=allowed username=%s group=%s permission=%s signature=RS256\n' \
    "$username" "$expected_group" "$expected_permission"
}

section "preflight"
for container in "$dc" "$casdoor" "$dirwatch" "$consumer"; do
  test "$("$docker_cmd" inspect --format '{{.State.Status}}' "$container")" = running
done
command -v jq >/dev/null
command -v openssl >/dev/null
container_env "$dirwatch" CASDOOR_DIRWATCH_MANAGED_GROUPS | grep -Eq '(^|,)APP_nextcloud(,|$)'
test "$(container_env "$casdoor" ANAS_IAM_CLIENT__NEXTCLOUD__INTERFACE)" = oidc
issuer=$(container_env "$casdoor" CASDOOR_DOMAIN_FULL)
client_id=$(container_env "$casdoor" ANAS_IAM_CLIENT__NEXTCLOUD__CLIENT_ID)
redirect_uri=$(container_env "$casdoor" ANAS_IAM_CLIENT__NEXTCLOUD__REDIRECT_URIS)
application=app-anas-nextcloud
anchor_claim=$(container_env "$casdoor" SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE)
discovery="$workdir/discovery.json"
consumer_curl "$issuer/.well-known/openid-configuration" >"$discovery"
jwks_uri=$(jq -er '.jwks_uri' "$discovery")
test "$(jq -r '.issuer' "$discovery")" = "$issuer"
test "$(jq -r '.authorization_endpoint' "$discovery")" = "$issuer/login/oauth/authorize"
test "$(jq -r '.token_endpoint' "$discovery")" = "$issuer/api/login/oauth/access_token"
printf 'OIDC discovery and confidential Consumer registration are ready\n'

section "create Samba account and authorization matrix"
create_matrix_users
for user in "$direct_user" "$all_user" "$admin_user" "$denied_user" "$nested_user"; do
  wait_for_user_state "$user" \
    '.name == "'"$user"'" and .isForbidden == false and .isDeleted == false and (.id | length) > 0 and (.externalId | length) > 0' \
    'active directory shadow'
done
wait_for_user_state "$direct_user" '(.groups | index("anas/APP_nextcloud")) != null' 'direct group'
wait_for_user_state "$all_user" '(.groups | index("anas/APP_all")) != null' 'all-applications group'
wait_for_user_state "$admin_user" '(.groups | index("anas/Admins")) != null' 'administrator group'
wait_for_user_state "$nested_user" '(.groups | index("anas/APP_nextcloud")) != null' 'recursive group'
wait_for_user_state "$denied_user" '(.groups | length) == 0' 'no allowed group'
direct_sub=$(casdoor_user "$direct_user" | jq -er '.id')
direct_anchor=$(casdoor_user "$direct_user" | jq -er '.externalId')
printf 'Samba authorization matrix converged in Casdoor\n'

section "Authorization Code, RS256 claims, and application permissions"
login_allowed "$direct_user" APP_nextcloud user "$direct_sub" "$direct_anchor"
all_sub=$(casdoor_user "$all_user" | jq -er '.id')
all_anchor=$(casdoor_user "$all_user" | jq -er '.externalId')
login_allowed "$all_user" APP_all user "$all_sub" "$all_anchor"
admin_sub=$(casdoor_user "$admin_user" | jq -er '.id')
admin_anchor=$(casdoor_user "$admin_user" | jq -er '.externalId')
login_allowed "$admin_user" Admins app-admin "$admin_sub" "$admin_anchor"
nested_sub=$(casdoor_user "$nested_user" | jq -er '.id')
nested_anchor=$(casdoor_user "$nested_user" | jq -er '.externalId')
login_allowed "$nested_user" APP_nextcloud user "$nested_sub" "$nested_anchor"
expect_login_denied "$denied_user" application-group-policy
expect_login_denied "$disabled_user" directory-authentication
test "$(jq 'length' "$accounts")" = 4
printf 'all allowed identities materialized once; denied identities received no application account\n'

section "disable blocks new credentials and re-enable preserves identity"
samba_tool user disable "$direct_user" >/dev/null
wait_for_user_state "$direct_user" \
  '.isForbidden == true and (.groups | length) == 0' 'disabled and groups cleared'
expect_login_denied "$direct_user" disabled
samba_tool user enable "$direct_user" >/dev/null
wait_for_user_state "$direct_user" \
  '.isForbidden == false and .isDeleted == false and (.groups | index("anas/APP_nextcloud")) != null' \
  're-enabled with group restored'
login_allowed "$direct_user" APP_nextcloud user "$direct_sub" "$direct_anchor"

section "group removal stops issuance and re-add restores the same account"
samba_tool group removemembers APP_nextcloud "$direct_user" >/dev/null
wait_for_user_state "$direct_user" \
  '(.groups | index("anas/APP_nextcloud")) == null' 'direct group removed'
expect_login_denied "$direct_user" application-group-policy
samba_tool group addmembers APP_nextcloud "$direct_user" >/dev/null
wait_for_user_state "$direct_user" \
  '(.groups | index("anas/APP_nextcloud")) != null' 'direct group restored'
login_allowed "$direct_user" APP_nextcloud user "$direct_sub" "$direct_anchor"

section "administrator removal downgrades the existing application account"
samba_tool group addmembers APP_nextcloud "$admin_user" >/dev/null
wait_for_user_state "$admin_user" \
  '(.groups | index("anas/APP_nextcloud")) != null and (.groups | index("anas/Admins")) != null' \
  'administrator also admitted as application user'
samba_tool group removemembers Admins "$admin_user" >/dev/null
wait_for_user_state "$admin_user" \
  '(.groups | index("anas/Admins")) == null and (.groups | index("anas/APP_nextcloud")) != null' \
  'administrator privilege removed while application access remains'
login_allowed "$admin_user" APP_nextcloud user "$admin_sub" "$admin_anchor"
test "$(jq -r --arg anchor "$admin_anchor" '.[$anchor].permission' "$accounts")" = user

section "rename reuses the Consumer identity"
samba_tool user rename "$direct_user" --samaccountname="$renamed_user" >/dev/null
wait_for_user_state "$renamed_user" \
  '.id == "'"$direct_sub"'" and .externalId == "'"$direct_anchor"'" and (.groups | index("anas/APP_nextcloud")) != null' \
  'renamed with permanent identity'
login_allowed "$renamed_user" APP_nextcloud user "$direct_sub" "$direct_anchor" "$direct_user"
expect_login_denied "$direct_user" old-directory-name
test "$(jq 'length' "$accounts")" = 4
test "$(jq -r --arg anchor "$direct_anchor" '.[$anchor].username' "$accounts")" = "$renamed_user"
printf 'rename updated the existing Consumer account without creating a duplicate\n'

section "delete blocks credentials and clears access"
samba_tool user delete "$renamed_user" >/dev/null
wait_for_user_state "$renamed_user" \
  '.isForbidden == true and .isDeleted == true and (.groups | length) == 0' \
  'deleted and groups cleared'
expect_login_denied "$renamed_user" deleted

printf '\nCasdoor OIDC Authorization Code E2E tests passed\n'
