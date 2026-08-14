#!/usr/bin/env bash
set -euo pipefail

# Exercise the complete browser-facing Authentik authorization-code flow for
# both OIDC consumers. This deliberately does not call either application's
# backend API to manufacture a session: the cookies must be created by the
# real redirects, provider login, code exchange, and application callback.

socket=${ANAS_TEST_DOCKER_SOCKET:-/run/anas-anchor-docker.sock}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_anchor_}
entry_ip=${ANAS_TEST_ENTRY_IP:-10.252.0.2}
entry_port=${ANAS_TEST_ENTRY_PORT:-9000}
domain=${ANAS_TEST_DOMAIN:-nas.test}
username=${ANAS_TEST_USERNAME:-admin}
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

curl_auth() {
  curl -skS --connect-timeout 10 --max-time 90 "${resolve[@]}" \
    -c "$cookie_jar" -b "$cookie_jar" "$@"
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
  expect_component xak-flow-redirect

  authenticated_redirect=$(absolute_url "$api_url" "$(jq -r '.to // "/if/user/"' "$body")")
  authentik_final=$(curl_auth -L -D "$headers" -o "$body" -w '%{url_effective}' "$authenticated_redirect")
  printf '%s_application_authorization_path=%s\n' "$app" "${authentik_final%%\?*}"

  if [[ "$authentik_final" == */if/flow/* ]]; then
    authorization_api=$(flow_api_url "$authentik_final")
    curl_auth -H 'Accept: application/json' -o "$body" "$authorization_api"
    authorization_component=$(json_component)
    printf '%s_authorization_component=%s\n' "$app" "$authorization_component"
    if [ "$authorization_component" = ak-stage-access-denied ]; then
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
  printf '%s_oidc_callback_complete final_url=%s\n' "$app" "$login_final_url"
}

wait_for_oidc_providers
oidc_login nextcloud "$nextcloud_url/apps/user_oidc/login/1"
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

docker -H "unix://$socket" exec "${prefix}samba_dc" samba-tool user show "$username" \
  --attributes=sAMAccountName,displayName,anasIdentityAnchor >"$workdir/ad-user"
ad_display_name=$(sed -n 's/^displayName: //p' "$workdir/ad-user")
anchor=$(sed -n 's/^anasIdentityAnchor: //p' "$workdir/ad-user")
test -n "$anchor"

ldap_mapping_anchor=$(docker -H "unix://$socket" exec "${prefix}postgres" sh -lc \
  'psql -U "$POSTGRES_USER" -d nextcloud -Atc "select directory_uuid from oc_ldap_user_mapping where owncloud_name='"'"'$1'"'"'"' \
  nextcloud-anchor "$username")
test "$ldap_mapping_anchor" = "$anchor"
user_json=$(docker -H "unix://$socket" exec -u www-data "${prefix}nextcloud" \
  php occ user:info "$nextcloud_session_id" --output=json)
test "$(printf '%s' "$user_json" | jq -r '.user_id')" = "$username"
test "$(printf '%s' "$user_json" | jq -r '.display_name')" = "$ad_display_name"
printf 'nextcloud_directory_identity=matched anchor=%s\n' "$anchor"

oidc_login meshcentral "$meshcentral_url/auth-oidc"
if ! grep -Fq 'id=MainMenuMyDevices' "$body"; then
  grep -Eo '<title>[^<]*|auth-oidc|messageid[^,;]*' "$body" | head -n 12 >&2 || true
  printf 'MeshCentral did not establish a logged-in application page; final URL: %s\n' "$login_final_url" >&2
  exit 1
fi
mesh_account=$(docker -H "unix://$socket" exec "${prefix}postgres" sh -lc \
  'psql -U "$POSTGRES_USER" -d meshcentral -Atc "select id || '"'"'|'"'"' || coalesce(doc->>'"'"'name'"'"','"'"''"'"') || '"'"'|'"'"' || coalesce(doc->>'"'"'siteadmin'"'"','"'"''"'"') from main where type='"'"'user'"'"' and id='"'"'user//~oidc:'"'"' || '"'"'$1'"'"'"' \
  meshcentral-anchor "$anchor")
IFS='|' read -r mesh_user_id mesh_display_name mesh_siteadmin <<<"$mesh_account"
test "$mesh_user_id" = "user//~oidc:$anchor"
iam_display_name=$(docker -H "unix://$socket" exec "${prefix}authentik" ak shell -c \
  "from authentik.core.models import User; print(User.objects.get(username='$username').name)" 2>/dev/null)
iam_display_name=$(printf '%s\n' "$iam_display_name" | tail -n 1 | tr -d '\r')
test -n "$iam_display_name"
test "$mesh_display_name" = "$iam_display_name"
test "$mesh_siteadmin" = "4294967295"
printf 'meshcentral_session=established id=%s name=%s admin_group=granted\n' \
  "$mesh_user_id" "$mesh_display_name"

connection_id=$(docker -H "unix://$socket" exec "${prefix}authentik" ak shell -c \
  "from authentik.sources.ldap.models import LDAPSource; from authentik.core.models import UserSourceConnection; s=LDAPSource.objects.get(slug='$authentik_ldap_source_slug'); c=UserSourceConnection.objects.get(source=s, user__username='$username'); print(c.identifier)" 2>/dev/null)
connection_id=$(printf '%s\n' "$connection_id" | tail -n 1 | tr -d '\r')
test "$connection_id" = "$anchor"

printf 'oidc_login_e2e=passed nextcloud_user=%s meshcentral_user=%s identity_anchor=matched\n' \
  "$nextcloud_session_id" "$mesh_user_id"
