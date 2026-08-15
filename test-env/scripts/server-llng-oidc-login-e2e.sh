#!/usr/bin/env bash
set -euo pipefail

docker_cmd=${DOCKER_CMD:-docker}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_anchor_}
entry_ip=${ANAS_TEST_ENTRY_IP:-10.252.0.2}
entry_port=${ANAS_TEST_ENTRY_PORT:-9000}
http_timeout=${ANAS_TEST_HTTP_TIMEOUT:-180}
domain=${ANAS_TEST_DOMAIN:-nas.test}
username=${ANAS_TEST_USERNAME:?ANAS_TEST_USERNAME is required}
expected_outcome=${ANAS_TEST_EXPECTED_OUTCOME:-allowed}
apps=${ANAS_TEST_APPS:-nextcloud,meshcentral}
expect_meshcentral_siteadmin=${ANAS_TEST_EXPECT_MESHCENTRAL_SITEADMIN:-false}
expect_app_admin=${ANAS_TEST_EXPECT_APP_ADMIN:-false}
: "${ANAS_TEST_PASSWORD:?ANAS_TEST_PASSWORD is required}"

nextcloud_url="https://nc.$domain:$entry_port"
meshcentral_url="https://meshcentral.$domain:$entry_port"
resolve=(
  --resolve "nc.$domain:$entry_port:$entry_ip"
  --resolve "meshcentral.$domain:$entry_port:$entry_ip"
  --resolve "auth.$domain:$entry_port:$entry_ip"
)
workdir=$(mktemp -d)
cookie_jar="$workdir/cookies"
headers="$workdir/headers"
body="$workdir/body"
trap 'rm -rf "$workdir"' EXIT HUP INT TERM
trap 'printf "FAIL: LLNG OIDC E2E line=%s command=%s\n" "$LINENO" "$BASH_COMMAND" >&2' ERR

curl_login() {
  curl -skS --connect-timeout 10 --max-time "$http_timeout" "${resolve[@]}" \
    -c "$cookie_jar" -b "$cookie_jar" "$@"
}

absolute_url() {
  python3 - "$1" "$2" <<'PY'
import sys
from urllib.parse import urljoin
print(urljoin(sys.argv[1], sys.argv[2]))
PY
}

login_outcome=
login_final_url=
llng_login() {
  local app=$1 start_url=$2 portal_url form_action form_payload
  rm -f "$cookie_jar" "$headers" "$body"
  portal_url=$(curl_login -L -D "$headers" -o "$body" -w '%{url_effective}' "$start_url")
  case "$portal_url" in
    "https://auth.$domain:$entry_port"/*) ;;
    *) printf '%s did not reach the LLNG portal: %s\n' "$app" "$portal_url" >&2; return 1 ;;
  esac

  readarray -t form < <(python3 - "$body" <<'PY'
import html.parser
import sys
import urllib.parse

class LoginForm(html.parser.HTMLParser):
    def __init__(self):
        super().__init__()
        self.action = ""
        self.in_form = False
        self.values = []
    def handle_starttag(self, tag, attrs):
        item = dict(attrs)
        if tag == "form" and not self.action:
            self.action = item.get("action", "")
            self.in_form = True
        elif tag == "input" and self.in_form and item.get("name"):
            if item.get("name") not in {"user", "password"}:
                self.values.append((item["name"], item.get("value", "")))
    def handle_endtag(self, tag):
        if tag == "form" and self.in_form:
            self.in_form = False

p = LoginForm()
with open(sys.argv[1], encoding="utf-8") as stream:
    p.feed(stream.read())
print(p.action)
print(urllib.parse.urlencode(p.values))
PY
  )
  test -n "${form[0]}"
  form_action=$(absolute_url "$portal_url" "${form[0]}")
  form_payload=${form[1]}
  login_final_url=$(curl_login -L -D "$headers" -o "$body" -w '%{url_effective}' \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    --data "$form_payload" --data-urlencode "user=$username" \
    --data-urlencode "password=$ANAS_TEST_PASSWORD" "$form_action")

  if [ "$expected_outcome" = auth-denied ]; then
    case "$login_final_url" in
      "https://auth.$domain:$entry_port"/*) ;;
      *) printf 'disabled user unexpectedly left the LLNG authentication portal\n' >&2; return 1 ;;
    esac
    grep -Eqi 'error|denied|invalid|failed|authentication' "$body"
    login_outcome=auth-denied
    printf '%s_login=denied username=%s reason=directory-authentication\n' "$app" "$username"
    return 0
  fi

  if [ "$expected_outcome" = policy-denied ]; then
    case "$login_final_url" in
      "$start_url"*) printf '%s unexpectedly established an application callback\n' "$app" >&2; return 1 ;;
    esac
    grep -Eqi 'denied|forbidden|not allowed|error' "$body"
    login_outcome=policy-denied
    printf '%s_login=denied username=%s reason=application-group-policy\n' "$app" "$username"
    return 0
  fi

  login_outcome=allowed
  printf '%s_oidc_callback_complete final_url=%s\n' "$app" "$login_final_url"
}

if [ "$expected_outcome" = allowed ]; then
  "$docker_cmd" exec "${prefix}samba_dc" samba-tool user show "$username" \
    --attributes=sAMAccountName,displayName,anasIdentityAnchor >"$workdir/ad-user"
  ad_display_name=$(sed -n 's/^displayName: //p' "$workdir/ad-user")
  anchor=$(sed -n 's/^anasIdentityAnchor: //p' "$workdir/ad-user")
  test -n "$anchor"
fi

if [[ ",$apps," == *,nextcloud,* ]]; then
  llng_login nextcloud "$nextcloud_url/apps/user_oidc/login/1"
  if [ "$expected_outcome" = allowed ]; then
    if ! grep -Eq 'data-user="|logout|logoutURL' "$body"; then
      grep -Eo '<title>[^<]*|<h[12][^>]*>[^<]*|class="error[^<]*|<p[^>]*>[^<]*' "$body" \
        | head -n 12 >&2 || true
      printf 'Nextcloud did not establish a logged-in page; final URL: %s\n' "$login_final_url" >&2
      exit 1
    fi
    nextcloud_session_id=$(sed -n 's/.*data-user="\([^"]*\)".*/\1/p' "$body" | head -n 1)
    test "$nextcloud_session_id" = "$username"
    session_json=$(curl_login -H 'OCS-APIRequest: true' "$nextcloud_url/ocs/v2.php/cloud/user?format=json")
    test "$(printf '%s' "$session_json" | jq -r '.ocs.data.id // empty')" = "$username"
    ldap_mapping_anchor=$("$docker_cmd" exec "${prefix}postgres" sh -lc \
      'psql -U "$POSTGRES_USER" -d nextcloud -A -t -F "|" -c "select owncloud_name, directory_uuid from oc_ldap_user_mapping"' \
      | grep -F "$username|" | head -n 1 | cut -d'|' -f2)
    test "$ldap_mapping_anchor" = "$anchor"
    user_json=$("$docker_cmd" exec -u www-data "${prefix}nextcloud" php occ user:info "$username" --output=json)
    test "$(printf '%s' "$user_json" | jq -r '.user_id')" = "$username"
    test "$(printf '%s' "$user_json" | jq -r '.display_name')" = "$ad_display_name"
    admin_probe_json=$(curl_login -H 'OCS-APIRequest: true' "$nextcloud_url/ocs/v1.php/cloud/users?format=json")
    if [ "$expect_app_admin" = true ]; then
      test "$(printf '%s' "$admin_probe_json" | jq -r '.ocs.meta.statuscode')" = "100"
    else
      test "$(printf '%s' "$admin_probe_json" | jq -r '.ocs.meta.statuscode')" != "100"
    fi
    printf 'nextcloud_admin_api=%s\n' "$expect_app_admin"
  fi
fi

if [ "$expected_outcome" = auth-denied ]; then
  test "$login_outcome" = auth-denied
  exit 0
fi

if [[ ",$apps," == *,meshcentral,* ]]; then
  llng_login meshcentral "$meshcentral_url/auth-oidc"
  if [ "$expected_outcome" = allowed ]; then
    # MeshCentral's page markup changes between releases.  The durable login
    # contract is the server-side account keyed by the directory anchor below,
    # not a particular menu element in the rendered HTML.
    mesh_account=
    for attempt in $(seq 1 30); do
      mesh_account=$("$docker_cmd" exec "${prefix}postgres" sh -lc \
        'psql -U "$POSTGRES_USER" -d meshcentral -A -t -F "|" -c "select id, doc from main where type = chr(117) || chr(115) || chr(101) || chr(114)"' \
        | grep -F "user//~oidc:$anchor|" | head -n 1 || true)
      [ -n "$mesh_account" ] && break
      sleep 1
    done
    test -n "$mesh_account"
    mesh_user_id=${mesh_account%%|*}
    mesh_doc=${mesh_account#*|}
    mesh_display_name=$(printf '%s' "$mesh_doc" | jq -r '.name // ""')
    mesh_siteadmin=$(printf '%s' "$mesh_doc" | jq -r '.siteadmin // ""')
    printf 'meshcentral_account id=%s name=%s siteadmin=%s\n' \
      "$mesh_user_id" "$mesh_display_name" "$mesh_siteadmin"
    test "$mesh_user_id" = "user//~oidc:$anchor"
    test "$mesh_display_name" = "$ad_display_name"
    if [ "$expect_meshcentral_siteadmin" = true ]; then
      test "$mesh_siteadmin" = 4294967295
    else
      test "$mesh_siteadmin" != 4294967295
    fi
  fi
fi

printf 'PASS: LLNG OIDC login username=%s outcome=%s apps=%s anchor=%s\n' \
  "$username" "$expected_outcome" "$apps" "${anchor:-not-issued}"
