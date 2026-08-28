#!/usr/bin/env bash
# TEST_CASES: MCO-T-002
set -euo pipefail

# Exercise the complete browser-facing Authentik authorization-code flow for
# both OIDC consumers. This deliberately does not call either application's
# backend API to manufacture a session: the cookies must be created by the
# real redirects, provider login, code exchange, and application callback.

socket=${ANAS_TEST_DOCKER_SOCKET:-/run/anas-anchor-docker.sock}
export ANAS_TEST_DOCKER_SOCKET=$socket
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"
source "$script_dir/server-meshcentral-oidc-only-e2e-lib.sh"
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_anchor_}
entry_ip=${ANAS_TEST_ENTRY_IP:-10.252.0.2}
entry_port=${ANAS_TEST_ENTRY_PORT:-9000}
http_timeout=${ANAS_TEST_HTTP_TIMEOUT:-180}
domain=${ANAS_TEST_DOMAIN:-nas.test}
username=${ANAS_TEST_USERNAME:-admin}
expected_outcome=${ANAS_TEST_EXPECTED_OUTCOME:-allowed}
apps=${ANAS_TEST_APPS:-nextcloud,meshcentral}
expect_meshcentral_siteadmin=${ANAS_TEST_EXPECT_MESHCENTRAL_SITEADMIN:-false}
expect_app_admin=${ANAS_TEST_EXPECT_APP_ADMIN:-false}
logout_mode=${ANAS_TEST_LOGOUT_MODE:-none}
authentik_ldap_source_slug=${ANAS_TEST_AUTHENTIK_LDAP_SOURCE_SLUG:-samba-ad}
: "${ANAS_TEST_PASSWORD:?ANAS_TEST_PASSWORD is required}"

nextcloud_host="nc.$domain"
meshcentral_host="meshcentral.$domain"
authentik_host="auth.$domain"
nextcloud_url="https://$nextcloud_host:$entry_port"
meshcentral_url="https://$meshcentral_host:$entry_port"
resolve=(
  --resolve "$nextcloud_host:$entry_port:$entry_ip"
  --resolve "$meshcentral_host:$entry_port:$entry_ip"
  --resolve "$authentik_host:$entry_port:$entry_ip"
)
workdir=$(mktemp -d)
cookie_jar="$workdir/cookies"
headers="$workdir/headers"
body="$workdir/body"
trap 'rm -rf "$workdir"' EXIT HUP INT TERM
trap 'printf "FAIL: Authentik OIDC E2E line=%s command=%s\n" "$LINENO" "$BASH_COMMAND" >&2' ERR

curl_auth() {
  curl -skS --connect-timeout 10 --max-time "$http_timeout" "${resolve[@]}" \
    -c "$cookie_jar" -b "$cookie_jar" "$@"
}

verify_nextcloud_logout() {
	local attempt final meta_status session_after status
  case "$logout_mode" in
    browser)
      printf '== end Authentik browser session and wait for OIDC back-channel logout ==\n'
      curl_auth -L -o "$body" "https://$authentik_host:$entry_port/if/flow/default-invalidation-flow/"
      ;;
    admin)
      printf '== administratively revoke Authentik session and wait for OIDC back-channel logout ==\n'
      docker -H "unix://$socket" exec "${prefix}authentik" ak shell -c \
        "from authentik.core.models import AuthenticatedSession; sessions=AuthenticatedSession.objects.filter(user__username='$username'); assert sessions.exists(); sessions.delete()" \
        >/dev/null
      ;;
    none) return 0 ;;
    *) printf 'unsupported ANAS_TEST_LOGOUT_MODE: %s\n' "$logout_mode" >&2; return 2 ;;
  esac
  for attempt in $(seq 1 30); do
    status=$(curl_auth -o "$body" -w '%{http_code}' -H 'OCS-APIRequest: true' \
      "$nextcloud_url/ocs/v2.php/cloud/user?format=json" || true)
    session_after=$(jq -r '.ocs.data.id // empty' "$body" 2>/dev/null || true)
    meta_status=$(jq -r '.ocs.meta.statuscode // empty' "$body" 2>/dev/null || true)
    if [ "$status" != 000 ] && [ -n "$meta_status" ] && [ "$session_after" != "$username" ]; then
      printf 'nextcloud_session=revoked mode=%s status=%s\n' "$logout_mode" "$status"
      final=$(curl_auth -L -o "$body" -w '%{url_effective}' "$nextcloud_url/apps/user_oidc/login/1")
      case "$final" in
        "https://$authentik_host:$entry_port"/if/flow/default-authentication-flow/*)
          printf 'silent_reauthentication=blocked mode=%s\n' "$logout_mode"
          return 0
          ;;
      esac
      printf 'Nextcloud silently restored a session after Authentik %s logout: %s\n' "$logout_mode" "$final" >&2
      return 1
    fi
    sleep 1
  done
  printf 'Nextcloud session remained authenticated after Authentik %s logout\n' "$logout_mode" >&2
  return 1
}

wait_for_oidc_providers() {
  local attempt
  for attempt in $(seq 1 30); do
    if docker -H "unix://$socket" exec "${prefix}authentik" ak shell -c \
      'from authentik.providers.oauth2.models import OAuth2Provider; required={"authorization_code", "refresh_token"}; assert all(required.issubset(set(OAuth2Provider.objects.get(name=name).grant_types)) for name in ("nextcloud", "meshcentral"))' \
      >/dev/null 2>&1; then
      printf 'authentik_oidc_providers=ready\n'
      return 0
    fi
    sleep 2
  done
  printf 'Authentik OIDC providers did not receive the required grant types\n' >&2
  return 1
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

absolute_url() {
  python3 - "$1" "$2" <<'PY'
import sys
from urllib.parse import urljoin
print(urljoin(sys.argv[1], sys.argv[2]))
PY
}

flow_api_url() {
  python3 - "$1" <<'PY'
import sys
from urllib.parse import urlencode, urlsplit, urlunsplit

parts = urlsplit(sys.argv[1])
path = parts.path.replace("/if/flow/", "/api/v3/flows/executor/", 1)
print(urlunsplit((parts.scheme, parts.netloc, path, urlencode({"query": parts.query}), "")))
PY
}

login_final_url=
login_outcome=
oidc_login() {
  local app=$1 start_url=$2 flow_url api_url authenticated_redirect
  local authentik_final authorization_api authorization_component consent_token
  local meta_redirect

  rm -f "$cookie_jar" "$headers" "$body"
  printf '== start %s OIDC login ==\n' "$app"
  flow_url=$(curl_auth -L -D "$headers" -o "$body" -w '%{url_effective}' "$start_url")
  printf '%s_authentication_flow_path=%s\n' "$app" "${flow_url%%\?*}"
  case "$flow_url" in
    */if/flow/default-authentication-flow/*) ;;
    *)
      printf '%s did not reach the Authentik authentication flow: %s\n' "$app" "$flow_url" >&2
      return 1
      ;;
  esac

  api_url=$(flow_api_url "$flow_url")
  curl_auth -H 'Accept: application/json' -o "$body" "$api_url"
  expect_component ak-stage-identification
  curl_auth -H 'Accept: application/json' -H 'Content-Type: application/json' \
    -L --data "$(jq -cn --arg username "$username" '{uid_field:$username}')" \
    -D "$headers" -o "$body" "$api_url"
  expect_component ak-stage-password
  curl_auth -H 'Accept: application/json' -H 'Content-Type: application/json' \
    -L --data "$(jq -cn --arg password "$ANAS_TEST_PASSWORD" '{password:$password}')" \
    -D "$headers" -o "$body" "$api_url"
  if [ "$expected_outcome" = auth-denied ]; then
    if [ "$(json_component)" = xak-flow-redirect ]; then
      printf '%s unexpectedly authenticated a disabled directory account\n' "$app" >&2
      return 1
    fi
    test "$(json_component)" = ak-stage-password
    # Authentik 2026.5 keeps the password component active for an invalid AD
    # bind, but records the failure as a flow message/audit event rather than
    # consistently returning a non-empty top-level `errors` array.  Require
    # the authoritative login_failed audit event so a changed JSON rendering
    # cannot turn a real authentication failure into a false-negative E2E.
    docker -H "unix://$socket" exec "${prefix}authentik" ak shell -c \
      "from authentik.events.models import Event; assert Event.objects.filter(action='login_failed', context__icontains='\"username\": \"$username\"').exists()" \
      >/dev/null 2>&1
    printf '%s_login=denied username=%s reason=directory-authentication\n' "$app" "$username"
    login_outcome=auth-denied
    return 0
  fi
  expect_component xak-flow-redirect

  authenticated_redirect=$(absolute_url "$api_url" "$(jq -r '.to // "/if/user/"' "$body")")
  authentik_final=$(curl_auth -L -D "$headers" -o "$body" -w '%{url_effective}' "$authenticated_redirect")
  printf '%s_application_authorization_path=%s\n' "$app" "${authentik_final%%\?*}"

  authorization_status=$(awk '/^HTTP\// { code=$2 } END { print code }' "$headers")
  if [ "$expected_outcome" = policy-denied ]; then
    if { [ "$authorization_status" = 403 ] || [ "$authorization_status" = 404 ]; } || \
      grep -Eqi 'access[ -]denied|permission[ -]denied|not authorized|ak-stage-access-denied' "$body"; then
      printf '%s_login=denied username=%s reason=application-group-policy\n' "$app" "$username"
      login_outcome=policy-denied
      return 0
    fi
  fi

  if [[ "$authentik_final" == */if/flow/* ]]; then
    authorization_api=$(flow_api_url "$authentik_final")
    curl_auth -H 'Accept: application/json' -o "$body" "$authorization_api"
    authorization_component=$(json_component)
    printf '%s_authorization_component=%s\n' "$app" "$authorization_component"
    if [ "$authorization_component" = ak-stage-access-denied ]; then
      if [ "$expected_outcome" = policy-denied ]; then
        printf '%s_login=denied username=%s reason=application-group-policy\n' "$app" "$username"
        login_outcome=policy-denied
        return 0
      fi
      printf '%s OIDC login was denied by the application policy\n' "$app" >&2
      return 1
    fi
    if [ "$authorization_component" = ak-stage-consent ]; then
      consent_token=$(jq -r '.token' "$body")
      curl_auth -H 'Accept: application/json' -H 'Content-Type: application/json' \
        --data "$(jq -cn --arg token "$consent_token" '{component:"ak-stage-consent",token:$token}')" \
        -o "$body" "$authorization_api"
      authorization_component=$(json_component)
    fi
    if [ "$authorization_component" != xak-flow-redirect ]; then
      expect_component xak-flow-redirect
    fi
    authentik_final=$(curl_auth -L -D "$headers" -o "$body" -w '%{url_effective}' \
      "$(absolute_url "$authorization_api" "$(jq -r '.to' "$body")")")
  fi

  # MeshCentral completes a successful strategy callback with an HTML meta
  # refresh so its session cookie survives the final navigation. curl follows
  # HTTP redirects only, so perform that browser step explicitly when present.
  meta_redirect=$(sed -n 's/.*content=0;url="\([^"]*\)".*/\1/p' "$body" | head -n 1)
  if [ -n "$meta_redirect" ]; then
    authentik_final=$(curl_auth -L -D "$headers" -o "$body" -w '%{url_effective}' \
      "$(absolute_url "$authentik_final" "$meta_redirect")")
  fi

  login_final_url=$authentik_final
  login_outcome=allowed
  printf '%s_oidc_callback_complete final_url=%s\n' "$app" "$login_final_url"
}

wait_for_oidc_providers
if [[ ",$apps," == *,meshcentral,* ]]; then
  verify_meshcentral_oidc_only curl_auth "$meshcentral_url" \
    "$cookie_jar" "$headers" "$body"
fi
if [ "$expected_outcome" = auth-denied ]; then
  first_app=${apps%%,*}
  case "$first_app" in
    nextcloud) oidc_login nextcloud "$nextcloud_url/apps/user_oidc/login/1" ;;
    meshcentral) oidc_login meshcentral "$meshcentral_url/auth-oidc" ;;
    *) printf 'unsupported ANAS_TEST_APPS entry: %s\n' "$first_app" >&2; exit 2 ;;
  esac
  test "$login_outcome" = auth-denied
  exit 0
fi

if [ "$expected_outcome" = allowed ]; then
  docker -H "unix://$socket" exec "${prefix}samba_dc" samba-tool user show "$username" \
    --attributes=sAMAccountName,displayName,anasIdentityAnchor >"$workdir/ad-user"
  ad_display_name=$(sed -n 's/^displayName: //p' "$workdir/ad-user")
  anchor=$(sed -n 's/^anasIdentityAnchor: //p' "$workdir/ad-user")
  test -n "$anchor"
fi

if [[ ",$apps," == *,nextcloud,* ]]; then
oidc_login nextcloud "$nextcloud_url/apps/user_oidc/login/1"
if [ "$expected_outcome" = policy-denied ]; then
  test "$login_outcome" = policy-denied
else
if ! grep -Eq 'data-user="|logout|logoutURL' "$body"; then
  grep -Eo '<title>[^<]*|<h[12][^>]*>[^<]*|class="error[^<]*' "$body" | head -n 10 >&2 || true
  printf 'Nextcloud did not establish a logged-in page; final URL: %s\n' "$login_final_url" >&2
  exit 1
fi
nextcloud_session_id=$(sed -n 's/.*data-user="\([^"]*\)".*/\1/p' "$body" | head -n 1)
test "$nextcloud_session_id" = "$username"
session_json=$(curl_auth -H 'OCS-APIRequest: true' \
  "$nextcloud_url/ocs/v2.php/cloud/user?format=json")
test "$(printf '%s' "$session_json" | jq -r '.ocs.data.id // empty')" = "$username"
printf 'nextcloud_session=established user=%s\n' "$nextcloud_session_id"

ldap_mapping_anchor=$(docker -H "unix://$socket" exec "${prefix}postgres" sh -lc \
  'psql -U "$POSTGRES_USER" -d nextcloud -A -t -F "|" -c "select owncloud_name, directory_uuid from oc_ldap_user_mapping"' \
  | grep -F "$username|" | head -n 1 | cut -d'|' -f2)
test "$ldap_mapping_anchor" = "$anchor"
user_json=$(docker -H "unix://$socket" exec -u www-data "${prefix}nextcloud" \
  php occ user:info "$nextcloud_session_id" --output=json)
test "$(printf '%s' "$user_json" | jq -r '.user_id')" = "$username"
test "$(printf '%s' "$user_json" | jq -r '.display_name')" = "$ad_display_name"
admin_probe_json=$(curl_auth -H 'OCS-APIRequest: true' \
  "$nextcloud_url/ocs/v1.php/cloud/users?format=json")
if [ "$expect_app_admin" = true ]; then
  test "$(printf '%s' "$admin_probe_json" | jq -r '.ocs.meta.statuscode')" = "100"
else
  test "$(printf '%s' "$admin_probe_json" | jq -r '.ocs.meta.statuscode')" != "100"
fi
printf 'nextcloud_admin_api=%s\n' "$expect_app_admin"
printf 'nextcloud_directory_identity=matched anchor=%s\n' "$anchor"
verify_nextcloud_logout
fi
fi

if [[ ",$apps," == *,meshcentral,* ]]; then
oidc_login meshcentral "$meshcentral_url/auth-oidc"
if [ "$expected_outcome" = policy-denied ]; then
  test "$login_outcome" = policy-denied
else
# Validate the durable server-side account below.  MeshCentral does not keep a
# stable DOM marker for its logged-in menu across releases.
mesh_account=
for attempt in $(seq 1 30); do
  mesh_account=$(docker -H "unix://$socket" exec "${prefix}postgres" sh -lc \
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
iam_display_name=$(docker -H "unix://$socket" exec "${prefix}authentik" ak shell -c \
  "from authentik.core.models import User; print(User.objects.get(username='$username').name)" 2>/dev/null)
iam_display_name=$(printf '%s\n' "$iam_display_name" | tail -n 1 | tr -d '\r')
test "$iam_display_name" = "$ad_display_name"
test "$mesh_display_name" = "$ad_display_name"
if [ "$expect_meshcentral_siteadmin" = true ]; then
  test "$mesh_siteadmin" = "4294967295"
else
  test "$mesh_siteadmin" != "4294967295"
fi
printf 'meshcentral_session=established id=%s name=%s siteadmin=%s\n' \
  "$mesh_user_id" "$mesh_display_name" "$mesh_siteadmin"
fi
fi

if [ "$expected_outcome" = policy-denied ]; then
  printf 'oidc_login_e2e=passed username=%s outcome=policy-denied apps=%s\n' "$username" "$apps"
  exit 0
fi

connection_id=$(docker -H "unix://$socket" exec "${prefix}authentik" ak shell -c \
  "from authentik.sources.ldap.models import LDAPSource; from authentik.core.models import UserSourceConnection; s=LDAPSource.objects.get(slug='$authentik_ldap_source_slug'); c=UserSourceConnection.objects.get(source=s, user__username='$username'); print(c.identifier)" 2>/dev/null)
connection_id=$(printf '%s\n' "$connection_id" | tail -n 1 | tr -d '\r')
test "$connection_id" = "$anchor"

printf 'oidc_login_e2e=passed nextcloud_user=%s meshcentral_user=%s identity_anchor=matched\n' \
  "${nextcloud_session_id:-not-tested}" "${mesh_user_id:-not-tested}"
