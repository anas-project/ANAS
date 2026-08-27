#!/usr/bin/env bash
# TEST_CASES: VIK-T-005, VIK-T-006
set -Eeuo pipefail

# Browser-facing Authentik authorization-code and Vikunja callback E2E. The
# script never prints the directory password, authorization code, JWT, or API
# token. Run it only against the dedicated isolated Docker daemon.

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"

mode=${1:-authentik}
docker_cmd=${DOCKER_CMD:-docker}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_vik_}
domain=${ANAS_TEST_DOMAIN:-vikunja.test}
db_type=${ANAS_TEST_DB_TYPE:-postgres}
entry_ip=${ANAS_TEST_ENTRY_IP:-10.253.20.2}
entry_port=${ANAS_TEST_ENTRY_PORT:-9000}
http_timeout=${ANAS_TEST_HTTP_TIMEOUT:-180}
matrix_timeout=${ANAS_TEST_MATRIX_TIMEOUT:-420}
matrix_suffix=${ANAS_TEST_MATRIX_SUFFIX:-$(date +%H%M%S)}
matrix_password=${ANAS_TEST_MATRIX_PASSWORD:-Vk-$(openssl rand -hex 18)-Aa1!}
preserve_users=${ANAS_TEST_PRESERVE_USERS:-false}
secret_file=${ANAS_TEST_SECRET_FILE:-}

dc="${prefix}samba_dc"
authentik="${prefix}authentik"
direct_user="vkd${matrix_suffix}"
all_user="vka${matrix_suffix}"
admin_user="vkm${matrix_suffix}"
denied_user="vkn${matrix_suffix}"
disabled_user="vkx${matrix_suffix}"
matrix_users=("$direct_user" "$all_user" "$admin_user" "$denied_user" "$disabled_user")

vikunja_host="tasks.$domain"
authentik_host="auth.$domain"
vikunja_url="https://$vikunja_host:$entry_port"
resolve=(
  --resolve "$vikunja_host:$entry_port:$entry_ip"
  --resolve "$authentik_host:$entry_port:$entry_ip"
)

workdir=$(mktemp -d)
chmod 700 "$workdir"
cookie_jar="$workdir/cookies"
headers="$workdir/headers"
body="$workdir/body"
callback="$workdir/callback.json"
trap 'rm -rf "$workdir"' EXIT HUP INT TERM
trap 'printf "FAIL: Vikunja OIDC E2E mode=%s line=%s\n" "$mode" "$LINENO" >&2' ERR

curl_browser() {
  curl -skS --connect-timeout 10 --max-time "$http_timeout" "${resolve[@]}" \
    -c "$cookie_jar" -b "$cookie_jar" "$@"
}

dc_exec() {
  "$docker_cmd" exec "$dc" "$@"
}

cleanup_users() {
  local user
  [ "$preserve_users" = true ] && return 0
  for user in "${matrix_users[@]}"; do
    dc_exec samba-tool user delete "$user" >/dev/null 2>&1 || true
  done
}

trap 'cleanup_users; rm -rf "$workdir"' EXIT HUP INT TERM

wait_anchor() {
  local user=$1 deadline anchor
  deadline=$(( $(date +%s) + matrix_timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    anchor=$(dc_exec samba-tool user show "$user" --attributes=anasIdentityAnchor 2>/dev/null |
      sed -n 's/^anasIdentityAnchor: //p')
    [ -n "$anchor" ] && return 0
    sleep 2
  done
  printf 'identity anchor was not written for %s\n' "$user" >&2
  return 1
}

create_users() {
  local user
  for user in "${matrix_users[@]}"; do
    dc_exec samba-tool user delete "$user" >/dev/null 2>&1 || true
    dc_exec samba-tool user add "$user" "$matrix_password" --userou='OU=People' \
      --mail-address="$user@$domain" >/dev/null 2>&1
    dc_exec samba-tool user setexpiry "$user" --noexpiry >/dev/null 2>&1
    dc_exec samba-tool user rename "$user" --display-name="Vikunja E2E $user" >/dev/null 2>&1
  done
  dc_exec samba-tool group addmembers APP_vikunja "$direct_user" >/dev/null 2>&1
  dc_exec samba-tool group addmembers APP_all "$all_user" >/dev/null 2>&1
  dc_exec samba-tool group addmembers Admins "$admin_user" >/dev/null 2>&1
  dc_exec samba-tool group addmembers APP_vikunja "$disabled_user" >/dev/null 2>&1
  dc_exec samba-tool user disable "$disabled_user" >/dev/null 2>&1
  for user in "${matrix_users[@]}"; do
    wait_anchor "$user"
  done
}

database_user_present() {
  local username=$1
  [[ "$username" =~ ^[A-Za-z0-9._-]+$ ]]
  case "$db_type" in
    postgres)
      "$docker_cmd" exec "${prefix}postgres" sh -lc \
        'psql -U "$POSTGRES_USER" -d vikunja -A -t -c "select username from users"' |
        grep -Fxq "$username"
      ;;
    mariadb)
      "$docker_cmd" exec "${prefix}mariadb" sh -lc \
        'mariadb -u root -p"$MARIADB_ROOT_PASSWORD" -N -s -e "select username from vikunja.users"' |
        grep -Fxq "$username"
      ;;
    *)
      printf 'unsupported ANAS_TEST_DB_TYPE: %s\n' "$db_type" >&2
      return 2
      ;;
  esac
}

wait_authentik_users() {
  local deadline users_csv
  users_csv=$(IFS=,; printf '%s' "${matrix_users[*]}")
  "$docker_cmd" exec "$authentik" ak shell -c \
    "from authentik.tasks.schedules.models import Schedule; [s.send() for s in Schedule.objects.filter(actor_name='authentik.sources.ldap.tasks.ldap_sync') if getattr(s.rel_obj, 'slug', None) == 'samba-ad']" \
    >/dev/null 2>&1
  deadline=$(( $(date +%s) + matrix_timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if "$docker_cmd" exec "$authentik" ak shell -c \
      "from authentik.core.models import User, UserSourceConnection; from authentik.sources.ldap.models import LDAPSource; s=LDAPSource.objects.get(slug='samba-ad'); names='$users_csv'.split(','); assert all(UserSourceConnection.objects.filter(source=s,user__username=n).exists() for n in names); assert User.objects.get(username='$direct_user').ak_groups.filter(name='APP_vikunja').exists(); assert User.objects.get(username='$all_user').ak_groups.filter(name='APP_all').exists(); assert User.objects.get(username='$admin_user').ak_groups.filter(name='Admins').exists()" \
      >/dev/null 2>&1; then
      printf 'authentik_directory_sync=ready users=%s\n' "${#matrix_users[@]}"
      return 0
    fi
    sleep 3
  done
  printf 'Authentik did not synchronize all Vikunja matrix users\n' >&2
  return 1
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

absolute_url() {
  python3 - "$1" "$2" <<'PY'
import sys
from urllib.parse import urljoin
print(urljoin(sys.argv[1], sys.argv[2]))
PY
}

query_value() {
  python3 - "$1" "$2" <<'PY'
import sys
from urllib.parse import parse_qs, urlsplit
print(parse_qs(urlsplit(sys.argv[1]).query).get(sys.argv[2], [""])[0])
PY
}

safe_url_path() {
  python3 - "$1" <<'PY'
import sys
from urllib.parse import urlsplit
parts = urlsplit(sys.argv[1])
print(f"{parts.scheme}://{parts.netloc}{parts.path}")
PY
}

authorize_url() {
  local auth_url state=$1
  auth_url=$(curl_browser "$vikunja_url/api/v1/info" |
    jq -r '.auth.openid_connect.providers[] | select(.key == "anas") | .auth_url')
  python3 - "$auth_url" "$state" "$vikunja_url/auth/openid/anas" <<'PY'
import sys
from urllib.parse import urlencode

params = {
    "client_id": "vikunja",
    "redirect_uri": sys.argv[3],
    "response_type": "code",
    "scope": "openid profile email",
    "state": sys.argv[2],
}
print(sys.argv[1] + "?" + urlencode(params))
PY
}

json_component() {
  jq -r '.component // empty' "$body"
}

expect_component() {
  local want=$1 got
  got=$(json_component)
  if [ "$got" != "$want" ]; then
    jq '{component,type,errors,to}' "$body" >&2 || true
    printf 'expected Authentik component %s, got %s\n' "$want" "$got" >&2
    return 1
  fi
}

persist_direct_session() {
  local jwt=$1 target_dir
  [ -n "$secret_file" ] || return 0
  target_dir=$(dirname -- "$secret_file")
  install -d -m 0700 "$target_dir"
  umask 077
  {
    printf 'ANAS_TEST_USERNAME=%q\n' "$direct_user"
    printf 'ANAS_TEST_PASSWORD=%q\n' "$matrix_password"
    printf 'ANAS_TEST_JWT=%q\n' "$jwt"
  } >"$secret_file"
  chmod 0600 "$secret_file"
  printf 'session_artifact=stored mode=0600\n'
}

login_user() {
  local username=$1 expected=$2 save_session=${3:-false}
  local state start flow_url api_url authenticated_redirect final authorization_api component
  local consent_token returned_state code jwt status authorization_status

  rm -f "$cookie_jar" "$headers" "$body" "$callback"
  printf 'oidc_flow=start user=%s expected=%s\n' "$username" "$expected"
  state=$(openssl rand -hex 16)
  start=$(authorize_url "$state")
  flow_url=$(curl_browser -L -D "$headers" -o "$body" -w '%{url_effective}' "$start")
  case "$flow_url" in
    */if/flow/default-authentication-flow/*) ;;
    *) printf 'authorization did not reach Authentik login flow\n' >&2; return 1 ;;
  esac
  printf 'oidc_flow=authentication user=%s\n' "$username"

  api_url=$(flow_api_url "$flow_url")
  curl_browser -H 'Accept: application/json' -o "$body" "$api_url"
  expect_component ak-stage-identification
  curl_browser -L -H 'Accept: application/json' -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg username "$username" '{uid_field:$username}')" \
    -D "$headers" -o "$body" "$api_url"
  printf 'oidc_flow=identification-submitted user=%s\n' "$username"
  expect_component ak-stage-password
  curl_browser -L -H 'Accept: application/json' -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg password "$matrix_password" '{password:$password}')" \
    -D "$headers" -o "$body" "$api_url"
  printf 'oidc_flow=password-submitted user=%s\n' "$username"

  if [ "$expected" = auth-denied ]; then
    test "$(json_component)" = ak-stage-password
    "$docker_cmd" exec "$authentik" ak shell -c \
      "from authentik.events.models import Event; assert Event.objects.filter(action='login_failed', context__icontains='\"username\": \"$username\"').exists()" \
      >/dev/null 2>&1
    printf 'oidc_login=denied user=%s reason=directory-authentication\n' "$username"
    return 0
  fi
  expect_component xak-flow-redirect

  authenticated_redirect=$(absolute_url "$api_url" "$(jq -r '.to' "$body")")
  final=$(curl_browser -L -D "$headers" -o "$body" -w '%{url_effective}' "$authenticated_redirect")
  authorization_status=$(awk '/^HTTP\// { code=$2 } END { print code }' "$headers")
  printf 'oidc_flow=authorization user=%s final_path=%s status=%s\n' \
    "$username" "$(safe_url_path "$final")" "$authorization_status"
  if [ "$expected" = policy-denied ] && {
    [ "$authorization_status" = 403 ] || [ "$authorization_status" = 404 ] ||
    grep -Eqi 'access[ -]denied|permission[ -]denied|not authorized|ak-stage-access-denied' "$body"
  }; then
    printf 'oidc_login=denied user=%s reason=application-group-policy\n' "$username"
    return 0
  fi
  if [[ "$final" == */if/flow/* ]]; then
    authorization_api=$(flow_api_url "$final")
    curl_browser -H 'Accept: application/json' -o "$body" "$authorization_api"
    component=$(json_component)
    if [ "$component" = ak-stage-access-denied ]; then
      if [ "$expected" = policy-denied ]; then
        printf 'oidc_login=denied user=%s reason=application-group-policy\n' "$username"
        return 0
      fi
      printf 'allowed user was denied by Authentik application policy\n' >&2
      return 1
    fi
    if [ "$component" = ak-stage-consent ]; then
      consent_token=$(jq -r '.token' "$body")
      curl_browser -H 'Accept: application/json' -H 'Content-Type: application/json' \
        --data "$(jq -cn --arg token "$consent_token" '{component:"ak-stage-consent",token:$token}')" \
        -o "$body" "$authorization_api"
    fi
    expect_component xak-flow-redirect
    final=$(curl_browser -L -D "$headers" -o "$body" -w '%{url_effective}' \
      "$(absolute_url "$authorization_api" "$(jq -r '.to' "$body")")")
  fi

  if [ "$expected" = policy-denied ]; then
    code=$(query_value "$final" code)
    if [ -z "$code" ]; then
      printf 'policy denial ended without an authorization code but lacked an explicit denial marker\n' >&2
    else
      printf 'user expected policy denial but received an authorization code\n' >&2
    fi
    return 1
  fi
  code=$(query_value "$final" code)
  returned_state=$(query_value "$final" state)
  test -n "$code"
  test "$returned_state" = "$state"

  : >"$callback"
  chmod 0600 "$callback"
  status=$(curl_browser -o "$callback" -w '%{http_code}' \
    -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg code "$code" --arg redirect "$vikunja_url/auth/openid/anas" \
      '{code:$code,scope:"openid profile email",redirect_url:$redirect}')" \
    "$vikunja_url/api/v1/auth/openid/anas/callback")
  if [ "$status" != 200 ]; then
    printf 'Vikunja OIDC callback returned HTTP %s\n' "$status" >&2
    jq '{code,message}' "$callback" >&2 || true
    return 1
  fi
  jwt=$(jq -r '.token // empty' "$callback")
  test -n "$jwt"
  status=$(curl_browser -o "$body" -w '%{http_code}' \
    -H "Authorization: Bearer $jwt" "$vikunja_url/api/v1/user")
  test "$status" = 200
  test "$(jq -r '.username' "$body")" = "$username"
  database_user_present "$username"
  [ "$save_session" = true ] && persist_direct_session "$jwt"
  printf 'oidc_login=allowed user=%s jit_user=present state=verified callback=api\n' "$username"
}

llng_login_user() {
  local username=$1 expected=$2 save_session=${3:-false}
  local state start portal_url form_action form_payload final returned_state code jwt status

  rm -f "$cookie_jar" "$headers" "$body" "$callback"
  printf 'oidc_flow=start user=%s expected=%s provider=llng\n' "$username" "$expected"
  state=$(openssl rand -hex 16)
  start=$(authorize_url "$state")
  portal_url=$(curl_browser -L -D "$headers" -o "$body" -w '%{url_effective}' "$start")
  case "$portal_url" in
    "https://$authentik_host:$entry_port"/*) ;;
    *) printf 'authorization did not reach the LLNG portal: %s\n' "$(safe_url_path "$portal_url")" >&2; return 1 ;;
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
  final=$(curl_browser -L -D "$headers" -o "$body" -w '%{url_effective}' \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    --data "$form_payload" --data-urlencode "user=$username" \
    --data-urlencode "password=$matrix_password" "$form_action")

  if [ "$expected" = auth-denied ]; then
    case "$final" in
      "https://$authentik_host:$entry_port"/*) ;;
      *) printf 'disabled user unexpectedly left the LLNG portal\n' >&2; return 1 ;;
    esac
    grep -Eqi 'error|denied|invalid|failed|authentication' "$body"
    printf 'oidc_login=denied user=%s reason=directory-authentication provider=llng\n' "$username"
    return 0
  fi

  if [ "$expected" = policy-denied ]; then
    if code=$(query_value "$final" code) && [ -n "$code" ]; then
      printf 'user expected LLNG policy denial but received an authorization code\n' >&2
      return 1
    fi
    case "$final" in
      "https://$authentik_host:$entry_port"/*) ;;
      *) printf 'LLNG policy denial ended outside the portal: %s\n' "$(safe_url_path "$final")" >&2; return 1 ;;
    esac
    grep -Eqi 'denied|forbidden|not allowed|error' "$body"
    printf 'oidc_login=denied user=%s reason=application-group-policy provider=llng\n' "$username"
    return 0
  fi

  code=$(query_value "$final" code)
  returned_state=$(query_value "$final" state)
  test -n "$code"
  test "$returned_state" = "$state"
  : >"$callback"
  chmod 0600 "$callback"
  status=$(curl_browser -o "$callback" -w '%{http_code}' \
    -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg code "$code" --arg redirect "$vikunja_url/auth/openid/anas" \
      '{code:$code,scope:"openid profile email",redirect_url:$redirect}')" \
    "$vikunja_url/api/v1/auth/openid/anas/callback")
  if [ "$status" != 200 ]; then
    printf 'Vikunja OIDC callback returned HTTP %s\n' "$status" >&2
    jq '{code,message}' "$callback" >&2 || true
    return 1
  fi
  jwt=$(jq -r '.token // empty' "$callback")
  test -n "$jwt"
  status=$(curl_browser -o "$body" -w '%{http_code}' \
    -H "Authorization: Bearer $jwt" "$vikunja_url/api/v1/user")
  test "$status" = 200
  test "$(jq -r '.username' "$body")" = "$username"
  database_user_present "$username"
  [ "$save_session" = true ] && persist_direct_session "$jwt"
  printf 'oidc_login=allowed user=%s jit_user=present state=verified callback=api provider=llng\n' "$username"
}

case "$mode" in
  setup)
    : "${ANAS_TEST_MATRIX_SUFFIX:?ANAS_TEST_MATRIX_SUFFIX is required for setup mode}"
    : "${ANAS_TEST_MATRIX_PASSWORD:?ANAS_TEST_MATRIX_PASSWORD is required for setup mode}"
    preserve_users=true
    create_users
    printf 'Vikunja OIDC matrix users are ready suffix=%s\n' "$matrix_suffix"
    ;;
  setup-authentik)
    : "${ANAS_TEST_MATRIX_SUFFIX:?ANAS_TEST_MATRIX_SUFFIX is required for setup-authentik mode}"
    : "${ANAS_TEST_MATRIX_PASSWORD:?ANAS_TEST_MATRIX_PASSWORD is required for setup-authentik mode}"
    preserve_users=true
    create_users
    wait_authentik_users
    printf 'Vikunja Authentik matrix users are synchronized suffix=%s\n' "$matrix_suffix"
    ;;
  cleanup)
    : "${ANAS_TEST_MATRIX_SUFFIX:?ANAS_TEST_MATRIX_SUFFIX is required for cleanup mode}"
    preserve_users=false
    cleanup_users
    printf 'Vikunja OIDC matrix users removed suffix=%s\n' "$matrix_suffix"
    ;;
  authentik)
    create_users
    wait_authentik_users
    printf '\n== direct APP_vikunja ==\n'
    login_user "$direct_user" allowed true
    printf '\n== APP_all ==\n'
    login_user "$all_user" allowed
    printf '\n== Admins ==\n'
    login_user "$admin_user" allowed
    printf '\n== enabled user without application group ==\n'
    login_user "$denied_user" policy-denied
    printf '\n== disabled directory user ==\n'
    login_user "$disabled_user" auth-denied
    ;;
  llng)
    create_users
    printf '\n== direct APP_vikunja ==\n'
    llng_login_user "$direct_user" allowed true
    printf '\n== APP_all ==\n'
    llng_login_user "$all_user" allowed
    printf '\n== Admins ==\n'
    llng_login_user "$admin_user" allowed
    printf '\n== enabled user without application group ==\n'
    llng_login_user "$denied_user" policy-denied
    printf '\n== disabled directory user ==\n'
    llng_login_user "$disabled_user" auth-denied
    ;;
  refresh)
    : "${ANAS_TEST_USERNAME:?ANAS_TEST_USERNAME is required for refresh mode}"
    : "${ANAS_TEST_PASSWORD:?ANAS_TEST_PASSWORD is required for refresh mode}"
    matrix_password=$ANAS_TEST_PASSWORD
    direct_user=$ANAS_TEST_USERNAME
    login_user "$direct_user" allowed true
    ;;
  *)
    printf 'usage: %s {setup|setup-authentik|cleanup|authentik|llng|refresh}\n' "$0" >&2
    exit 2
    ;;
esac

printf '\nPASS: Vikunja OIDC E2E mode=%s\n' "$mode"
