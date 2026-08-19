#!/usr/bin/env bash
set -euo pipefail

socket=${ANAS_TEST_DOCKER_SOCKET:-/run/anas-anchor-docker.sock}
export ANAS_TEST_DOCKER_SOCKET=$socket
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_anchor_}
entry_ip=${ANAS_TEST_ENTRY_IP:-10.252.0.2}
entry_port=${ANAS_TEST_ENTRY_PORT:-9000}
domain=${ANAS_TEST_DOMAIN:-nas.test}
username=${ANAS_TEST_USERNAME:-admin}
expected_outcome=${ANAS_TEST_EXPECTED_OUTCOME:-allowed}
authentik_ldap_source_slug=${ANAS_TEST_AUTHENTIK_LDAP_SOURCE_SLUG:-samba-ad}
: "${ANAS_TEST_PASSWORD:?ANAS_TEST_PASSWORD is required}"

nextcloud_host="nc.$domain"
authentik_host="auth.$domain"
nextcloud_url="https://$nextcloud_host:$entry_port"
resolve=(
  --resolve "$nextcloud_host:$entry_port:$entry_ip"
  --resolve "$authentik_host:$entry_port:$entry_ip"
)
workdir=$(mktemp -d)
cookie_jar="$workdir/cookies"
headers="$workdir/headers"
body="$workdir/body"
trap 'rm -rf "$workdir"' EXIT HUP INT TERM

curl_auth() {
  curl -skS --connect-timeout 10 --max-time 60 "${resolve[@]}" \
    -c "$cookie_jar" -b "$cookie_jar" "$@"
}

json_component() {
  jq -r '.component // empty' "$body"
}

expect_component() {
  local want=$1 got
  got=$(json_component)
  if [ "$got" != "$want" ]; then
    jq '{component, type, errors, to}' "$body" >&2 || true
    sed -n '1,12p' "$headers" >&2 || true
    printf 'expected flow component %s, got %s\n' "$want" "$got" >&2
    return 1
  fi
}

follow_sp_request() {
  local location action
  curl_auth -D "$headers" -o "$body" \
    "$nextcloud_url/apps/user_saml/saml/login?idp=1"
  location=$(awk 'BEGIN { IGNORECASE=1 } /^location:/ { sub(/^[^:]*:[[:space:]]*/, ""); sub(/\r$/, ""); print; exit }' "$headers")
  if [ -n "$location" ]; then
    result_url=$(curl_auth -L -o "$body" -w '%{url_effective}' "$location")
    return
  fi

  readarray -t request_fields < <(python3 - "$body" "$nextcloud_url" <<'PY'
import html.parser
import sys
from urllib.parse import urljoin

class Form(html.parser.HTMLParser):
    def __init__(self):
        super().__init__()
        self.action = ""
        self.values = {}
    def handle_starttag(self, tag, attrs):
        item = dict(attrs)
        if tag == "form" and not self.action:
            self.action = item.get("action", "")
        if tag == "input" and item.get("name"):
            self.values[item["name"]] = item.get("value", "")

parser = Form()
with open(sys.argv[1], encoding="utf-8") as stream:
    parser.feed(stream.read())
print(urljoin(sys.argv[2], parser.action))
for name in ("SAMLRequest", "RelayState", "SigAlg", "Signature"):
    print(parser.values.get(name, ""))
PY
  )
  action=${request_fields[0]}
  test -n "$action"
  test -n "${request_fields[1]}"
  response_code=$(curl_auth -H 'Content-Type: application/x-www-form-urlencoded' \
    --data-urlencode "SAMLRequest=${request_fields[1]}" \
    --data-urlencode "RelayState=${request_fields[2]}" \
    --data-urlencode "SigAlg=${request_fields[3]}" \
    --data-urlencode "Signature=${request_fields[4]}" \
    -D "$headers" -o "$body" -w '%{http_code}' "$action")
  if [[ "$response_code" = 3* ]]; then
    location=$(awk 'BEGIN { IGNORECASE=1 } /^location:/ { sub(/^[^:]*:[[:space:]]*/, ""); sub(/\r$/, ""); print; exit }' "$headers")
    test -n "$location"
    printf 'saml_post_redirect_path=%s\n' "${location%%\?*}"
    location=$(python3 - "$action" "$location" <<'PY'
import sys
from urllib.parse import urljoin
print(urljoin(sys.argv[1], sys.argv[2]))
PY
    )
    result_url=$(curl_auth -L -o "$body" -w '%{url_effective}' "$location")
  else
    result_url=$action
  fi
}

printf '== start Nextcloud SAML login ==\n'
follow_sp_request
flow_url=$result_url
printf 'authentication_flow_path=%s\n' "${flow_url%%\?*}"
python3 - "$flow_url" <<'PY'
import sys
from urllib.parse import parse_qs, urlsplit
target = parse_qs(urlsplit(sys.argv[1]).query).get("next", [""])[0]
print("authentication_flow_next_path=" + urlsplit(target).path)
PY
api_url=$(python3 - "$flow_url" <<'PY'
import sys
from urllib.parse import urlencode, urlsplit, urlunsplit

parts = urlsplit(sys.argv[1])
path = parts.path.replace(
    "/if/flow/default-authentication-flow/",
    "/api/v3/flows/executor/default-authentication-flow/",
)
print(urlunsplit((parts.scheme, parts.netloc, path, urlencode({"query": parts.query}), parts.fragment)))
PY
)

curl_auth -H 'Accept: application/json' -o "$body" "$api_url"
expect_component ak-stage-identification
curl_auth -H 'Accept: application/json' -H 'Content-Type: application/json' \
  -L \
  --data "$(jq -cn --arg username "$username" '{uid_field:$username}')" \
  -D "$headers" -o "$body" "$api_url"
expect_component ak-stage-password
curl_auth -H 'Accept: application/json' -H 'Content-Type: application/json' \
  -L \
  --data "$(jq -cn --arg password "$ANAS_TEST_PASSWORD" '{password:$password}')" \
  -D "$headers" -o "$body" "$api_url"
if [ "$expected_outcome" = auth-denied ]; then
  if [ "$(json_component)" = xak-flow-redirect ]; then
    printf 'disabled directory account unexpectedly authenticated: %s\n' "$username" >&2
    exit 1
  fi
  expect_component ak-stage-password
  test "$(jq '.errors | length > 0' "$body")" = true
  printf 'login=denied username=%s reason=directory-authentication\n' "$username"
  exit 0
fi
expect_component xak-flow-redirect
printf 'authentication_complete_path=%s\n' "$(jq -r '.to // empty' "$body" | cut -d '?' -f 1)"
authenticated_redirect=$(python3 - "$api_url" "$(jq -r '.to // "/if/user/"' "$body")" <<'PY'
import sys
from urllib.parse import urljoin
print(urljoin(sys.argv[1], sys.argv[2]))
PY
)
authentik_final=$(curl_auth -L -D "$headers" -o "$body" -w '%{url_effective}' "$authenticated_redirect")
me_json=$(curl_auth "https://$authentik_host:$entry_port/api/v3/core/users/me/")
printf 'authentik_session_user=%s\n' "$(printf '%s' "$me_json" | jq -r '.user.username // "anonymous"')"
printf 'application_authorization_path=%s\n' "${authentik_final%%\?*}"
authorization_status=$(awk '/^HTTP\// { code=$2 } END { print code }' "$headers")
printf 'application_authorization_status=%s\n' "$authorization_status"
if [ "$expected_outcome" = denied ] && { [ "$authorization_status" = 403 ] || [ "$authorization_status" = 404 ]; }; then
  printf 'login=denied username=%s reason=application-group-policy\n' "$username"
  exit 0
fi
if [ "$expected_outcome" = denied ] && grep -Eqi 'access[ -]denied|permission[ -]denied|ak-stage-access-denied' "$body"; then
  printf 'login=denied username=%s reason=application-group-policy\n' "$username"
  exit 0
fi

if [[ "$authentik_final" == */if/flow/* ]]; then
  authorization_api=$(python3 - "$authentik_final" <<'PY'
import sys
from urllib.parse import urlencode, urlsplit, urlunsplit
parts = urlsplit(sys.argv[1])
path = parts.path.replace("/if/flow/", "/api/v3/flows/executor/", 1)
print(urlunsplit((parts.scheme, parts.netloc, path, urlencode({"query": parts.query}), "")))
PY
  )
  curl_auth -H 'Accept: application/json' -o "$body" "$authorization_api"
  authorization_component=$(json_component)
  printf 'authorization_flow_component=%s\n' "$authorization_component"
  if [ "$expected_outcome" = denied ]; then
    test "$authorization_component" = ak-stage-access-denied
    printf 'login=denied username=%s reason=application-group-policy\n' "$username"
    exit 0
  fi
  if [ "$authorization_component" = ak-stage-consent ]; then
    consent_token=$(jq -r '.token' "$body")
    curl_auth -H 'Accept: application/json' -H 'Content-Type: application/json' \
      --data "$(jq -cn --arg token "$consent_token" '{component:"ak-stage-consent",token:$token}')" \
      -o "$body" "$authorization_api"
    authorization_component=$(json_component)
  fi
  if [ "$authorization_component" = xak-flow-redirect ]; then
    assertion_redirect=$(python3 - "$authorization_api" "$(jq -r '.to' "$body")" <<'PY'
import sys
from urllib.parse import urljoin
print(urljoin(sys.argv[1], sys.argv[2]))
PY
    )
    authentik_final=$(curl_auth -L -D "$headers" -o "$body" -w '%{url_effective}' "$assertion_redirect")
  elif [ "$authorization_component" != ak-stage-autosubmit ]; then
    expect_component ak-stage-autosubmit
  fi
fi

test "$expected_outcome" = allowed

printf '== exchange signed SAML response ==\n'
readarray -t saml_fields < <(python3 - "$body" "$authentik_final" <<'PY'
import html.parser
import json
import sys
from urllib.parse import parse_qs, urlsplit

class Inputs(html.parser.HTMLParser):
    def __init__(self):
        super().__init__()
        self.values = {}
    def handle_starttag(self, tag, attrs):
        if tag == "input":
            item = dict(attrs)
            if item.get("name") in {"SAMLResponse", "RelayState"}:
                self.values[item["name"]] = item.get("value", "")

parser = Inputs()
with open(sys.argv[1], encoding="utf-8") as stream:
    raw = stream.read()
try:
    data = json.loads(raw)
    parser.values.update(data.get("attrs", {}))
except json.JSONDecodeError:
    parser.feed(raw)
query = parse_qs(urlsplit(sys.argv[2]).query)
print(parser.values.get("SAMLResponse") or query.get("SAMLResponse", [""])[0])
print(parser.values.get("RelayState") or query.get("RelayState", [""])[0])
PY
)
if [ -z "${saml_fields[0]}" ]; then
  grep -Eo '<title>[^<]*|component="[^"]*|class="pf-v5-c-alert[^<]*|ak-[a-z-]+|<form[^>]*|<input[^>]*name="[^\"]+"' "$body" | head -n 20 >&2 || true
  tail -n 20 "$headers" | grep -E '^(HTTP/|[Ll]ocation:|[Cc]ontent-[Tt]ype:)' >&2 || true
  printf 'Authentik returned no SAMLResponse form or redirect parameter; final path: %s\n' "${authentik_final%%\?*}" >&2
  exit 1
fi
printf 'saml_response=present bytes=%s\n' "${#saml_fields[0]}"
final_url=$(curl_auth -L -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode "SAMLResponse=${saml_fields[0]}" \
  --data-urlencode "RelayState=${saml_fields[1]}" \
  -o "$body" -w '%{url_effective}' "$nextcloud_url/apps/user_saml/saml/acs")
if ! grep -Eq 'data-user="|logout|logoutURL' "$body"; then
  grep -Eo '<title>[^<]*|<h[12][^>]*>[^<]*|class="error[^<]*' "$body" | head -n 10 >&2 || true
  printf 'Nextcloud did not establish a logged-in page; final path: %s\n' "${final_url#*://*/}" >&2
  exit 1
fi
printf 'nextcloud_session=established final_url=%s\n' "$final_url"

printf '== verify session identity and directory anchor ==\n'
session_id=$(sed -n 's/.*data-user="\([^"]*\)".*/\1/p' "$body" | head -n 1)
if [ -z "$session_id" ]; then
  grep -Eo 'data-(user|uid|user-id|user-displayname)="[^"]*"|<title>[^<]*' "$body" | head -n 20 >&2 || true
  printf 'logged-in dashboard did not expose a session user id\n' >&2
  exit 1
fi
session_json=$(curl_auth -H 'OCS-APIRequest: true' \
  "$nextcloud_url/ocs/v2.php/cloud/user?format=json")
ocs_session_id=$(printf '%s' "$session_json" | jq -r 'if (.ocs.data | type) == "object" then .ocs.data.id // empty else empty end')
if [ -n "$ocs_session_id" ]; then
  test "$ocs_session_id" = "$session_id"
fi
printf 'nextcloud_session_id=%s\n' "$session_id"

docker -H "unix://$socket" exec "${prefix}samba_dc" samba-tool user show "$username" \
  --attributes=sAMAccountName,displayName,anasIdentityAnchor >"$workdir/ad-user"
ad_username=$(sed -n 's/^sAMAccountName: //p' "$workdir/ad-user")
ad_display_name=$(sed -n 's/^displayName: //p' "$workdir/ad-user")
anchor=$(sed -n 's/^anasIdentityAnchor: //p' "$workdir/ad-user")
printf 'directory_username=%s directory_anchor=%s\n' "$ad_username" "$anchor"
test "$ad_username" = "$username"
test -n "$anchor"
test "$session_id" = "$username"

ldap_mapping_anchor=$(docker -H "unix://$socket" exec "${prefix}postgres" sh -lc \
  'psql -U "$POSTGRES_USER" -d nextcloud -Atc "select directory_uuid from oc_ldap_user_mapping where owncloud_name='"'"'$1'"'"'"' \
  nextcloud-anchor "$username")
test "$ldap_mapping_anchor" = "$anchor"

user_json=$(docker -H "unix://$socket" exec -u www-data "${prefix}nextcloud" \
  php occ user:info "$session_id" --output=json)
printf 'nextcloud_user_id=%s nextcloud_display_name=%s\n' \
  "$(printf '%s' "$user_json" | jq -r '.user_id // empty')" \
  "$(printf '%s' "$user_json" | jq -r '.display_name // empty')"
test "$(printf '%s' "$user_json" | jq -r '.user_id')" = "$username"
test "$(printf '%s' "$user_json" | jq -r '.display_name')" = "$ad_display_name"

connection_id=$(docker -H "unix://$socket" exec "${prefix}authentik" ak shell -c \
  "from authentik.sources.ldap.models import LDAPSource; from authentik.core.models import UserSourceConnection; s=LDAPSource.objects.get(slug='$authentik_ldap_source_slug'); c=UserSourceConnection.objects.get(source=s, user__username='$username'); print(c.identifier)" 2>/dev/null)
connection_id=$(printf '%s\n' "$connection_id" | tail -n 1 | tr -d '\r')
test "$connection_id" = "$anchor"

printf 'login=allowed username=%s id=%s identity_anchor=matched\n' "$username" "$session_id"
