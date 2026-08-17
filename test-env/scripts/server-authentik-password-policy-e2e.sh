#!/usr/bin/env bash
set -euo pipefail

if [ -n "${ANAS_TEST_DOCKER_SOCKET:-}" ]; then
  export DOCKER_HOST="unix://$ANAS_TEST_DOCKER_SOCKET"
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=server-iam-password-policy-common.sh
source "$script_dir/server-iam-password-policy-common.sh"

authentik=${ANAS_TEST_AUTHENTIK_CONTAINER:-${prefix}authentik}
entry_ip=${ANAS_TEST_ENTRY_IP:-10.252.0.2}
entry_port=${ANAS_TEST_ENTRY_PORT:-9000}
domain=${ANAS_TEST_DOMAIN:-nas.test}
http_timeout=${ANAS_TEST_HTTP_TIMEOUT:-180}
sync_timeout=${ANAS_TEST_SYNC_TIMEOUT:-420}
authentik_host="auth.$domain"
authentik_url="https://$authentik_host:$entry_port"
workdir=$(mktemp -d)
cookie_jar="$workdir/cookies"
body="$workdir/body"
headers="$workdir/headers"
resolve=(--resolve "$authentik_host:$entry_port:$entry_ip")

cleanup() {
  cleanup_password_policy_fixture
  rm -rf "$workdir"
}
trap cleanup EXIT HUP INT TERM
trap 'printf "FAIL: Authentik password-policy E2E line=%s command=%s\n" "$LINENO" "$BASH_COMMAND" >&2' ERR

curl_authentik() {
  curl -skS --connect-timeout 10 --max-time "$http_timeout" "${resolve[@]}" \
    -c "$cookie_jar" -b "$cookie_jar" "$@"
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

component() {
  jq -r '.component // empty' "$body"
}

wait_authentik_user() {
  local deadline
  "$docker_cmd" exec "$authentik" ak shell -c \
    "from authentik.tasks.schedules.models import Schedule; [s.send() for s in Schedule.objects.filter(actor_name='authentik.sources.ldap.tasks.ldap_sync') if getattr(s.rel_obj, 'slug', None) == 'samba-ad']" >/dev/null
  deadline=$(( $(date +%s) + sync_timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if "$docker_cmd" exec "$authentik" ak shell -c \
      "from authentik.core.models import User, UserSourceConnection; from authentik.sources.ldap.models import LDAPSource; u=User.objects.get(username='$policy_user'); s=LDAPSource.objects.get(slug='samba-ad'); assert UserSourceConnection.objects.filter(user=u, source=s).exists(); assert not u.has_usable_password()" \
      >/dev/null 2>&1; then
      return 0
    fi
    sleep 3
  done
  printf 'Authentik did not synchronize password-policy user %s\n' "$policy_user" >&2
  return 1
}

verify_authentik_policy() {
  "$docker_cmd" exec "$authentik" ak shell -c \
    "from authentik.policies.password.models import PasswordPolicy; from authentik.sources.ldap.models import LDAPSource; from authentik.stages.prompt.models import Prompt; p=PasswordPolicy.objects.get(name='default-password-change-password-policy'); assert p.length_min == $policy_min_length; assert not p.check_zxcvbn; assert (p.amount_digits,p.amount_uppercase,p.amount_lowercase,p.amount_symbols) == (0,0,0,0); s=LDAPSource.objects.get(slug='samba-ad'); assert s.sync_users_password and not s.password_login_update_internal_password; g=Prompt.objects.get(name='anas Samba AD password policy guidance').initial_value; assert str($policy_min_length) in g; assert str($policy_history) in g; assert str($policy_min_age) in g" >/dev/null
}

authentik_login() {
  local password=$1 expected=$2 ui api
  rm -f "$cookie_jar"
  ui=$(curl_authentik -L -o "$body" -w '%{url_effective}' \
    "$authentik_url/if/flow/default-authentication-flow/?next=/if/user/")
  api=$(flow_api_url "$ui")
  curl_authentik -H 'Accept: application/json' -o "$body" "$api"
  [ "$(component)" = ak-stage-identification ]
  curl_authentik -H 'Accept: application/json' -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg username "$policy_user" '{uid_field:$username}')" -o "$body" "$api"
  [ "$(component)" = ak-stage-password ]
  curl_authentik -H 'Accept: application/json' -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg password "$password" '{password:$password}')" -o "$body" "$api"
  if [ "$expected" = success ]; then
    [ "$(component)" = xak-flow-redirect ]
  else
    [ "$(component)" = ak-stage-password ]
  fi
}

open_password_change() {
  local ui
  ui=$(curl_authentik -L -o "$body" -w '%{url_effective}' \
    "$authentik_url/if/flow/default-password-change/")
  password_api=$(flow_api_url "$ui")
  curl_authentik -H 'Accept: application/json' -o "$body" "$password_api"
  [ "$(component)" = ak-stage-prompt ]
  jq -e --arg min "$policy_min_length" --arg history "$policy_history" --arg age "$policy_min_age" \
    '[.fields[] | select(.field_key == "anas_password_policy_guidance") | .initial_value][0] as $g | ($g | contains($min)) and ($g | contains($history)) and ($g | contains($age))' \
    "$body" >/dev/null
}

authentik_change() {
  local password=$1 repeat=$2 expected_component=$3 expected_pattern=${4:-}
  open_password_change
  curl_authentik -H 'Accept: application/json' -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg password "$password" --arg repeat "$repeat" \
      '{password:$password,password_repeat:$repeat}')" -o "$body" "$password_api"
  [ "$(component)" = "$expected_component" ]
  if [ -n "$expected_pattern" ]; then
    jq -c '.errors // []' "$body" | grep -Eqi "$expected_pattern"
  fi
}

assert_no_local_password() {
  "$docker_cmd" exec "$authentik" ak shell -c \
    "from authentik.core.models import User; assert not User.objects.get(username='$policy_user').has_usable_password()" >/dev/null
}

prepare_password_policy_fixture
wait_authentik_user
verify_authentik_policy
allow_rapid_password_changes

printf '\n== Authentik local preflight: minimum length ==\n'
authentik_login "$initial_password" success
authentik_change "$too_short_password" "$too_short_password" ak-stage-prompt 'too short|至少|must contain'

printf '\n== Authentik local preflight: matching confirmation ==\n'
authentik_change "$changed_password" "$final_password" ak-stage-prompt "match|一致"

printf '\n== Authentik AD preflight: complexity ==\n'
authentik_change "$complexity_password" "$complexity_password" ak-stage-prompt 'complexity|复杂'

printf '\n== Authentik successful Samba writeback and no local credential ==\n'
authentik_change "$changed_password" "$changed_password" xak-flow-redirect
assert_no_local_password
authentik_login "$initial_password" failure
authentik_login "$changed_password" success

printf '\n== Authentik Samba-final history rejection and safe error mapping ==\n'
authentik_change "$initial_password" "$initial_password" ak-stage-prompt 'Samba.*(rejected|拒绝)'
"$docker_cmd" exec "$authentik" ak shell -c \
  "from authentik.events.models import Event; assert Event.objects.filter(action='configuration_error', context__icontains='Failed to change password in LDAP source due to remote error').filter(context__icontains='$policy_user').exists()" >/dev/null
assert_no_local_password
authentik_login "$changed_password" success

printf '\n== Authentik remains usable after a rejected change ==\n'
authentik_change "$final_password" "$final_password" xak-flow-redirect
assert_no_local_password
authentik_login "$changed_password" failure
authentik_login "$final_password" success

printf 'PASS: Authentik Samba password-policy E2E user=%s min_length=%s history=%s min_age=%s\n' \
  "$policy_user" "$policy_min_length" "$policy_history" "$policy_min_age"
