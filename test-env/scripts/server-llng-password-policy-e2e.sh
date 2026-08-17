#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=server-iam-password-policy-common.sh
source "$script_dir/server-iam-password-policy-common.sh"

llng=${ANAS_TEST_LLNG_CONTAINER:-${prefix}llng}
entry_ip=${ANAS_TEST_ENTRY_IP:-10.253.0.2}
entry_port=${ANAS_TEST_ENTRY_PORT:-9000}
domain=${ANAS_TEST_DOMAIN:-llng.nas.test}
http_timeout=${ANAS_TEST_HTTP_TIMEOUT:-180}
portal_host="auth.$domain"
portal_url="https://$portal_host:$entry_port/"
workdir=$(mktemp -d)
cookie_jar="$workdir/cookies"
body="$workdir/body"
headers="$workdir/headers"
resolve=(--resolve "$portal_host:$entry_port:$entry_ip")

cleanup() {
  cleanup_password_policy_fixture
  rm -rf "$workdir"
}
trap cleanup EXIT HUP INT TERM
trap 'printf "FAIL: LLNG password-policy E2E line=%s command=%s\n" "$LINENO" "$BASH_COMMAND" >&2' ERR

curl_llng() {
  curl -skS --connect-timeout 10 --max-time "$http_timeout" "${resolve[@]}" \
    -c "$cookie_jar" -b "$cookie_jar" "$@"
}

verify_llng_policy() {
  local config catalog=/usr/share/lemonldap-ng/portal/htdocs/static/languages/zh.json
  config=$("$docker_cmd" exec "$llng" lemonldap-ng-cli -json get \
    passwordPolicyActivation portalDisplayPasswordPolicy passwordPolicyMinSize)
  printf '%s' "$config" | jq -e \
    --argjson min "$policy_min_length" \
    '(.passwordPolicyActivation | tonumber) == 1 and
     (.portalDisplayPasswordPolicy | tonumber) == 1 and
     (.passwordPolicyMinSize | tonumber) == $min' >/dev/null
  "$docker_cmd" exec "$llng" grep -Fq "新密码至少 ${policy_min_length} 个字符" "$catalog"
  "$docker_cmd" exec "$llng" grep -Fq "不能重复最近 ${policy_history} 个密码" "$catalog"
  "$docker_cmd" exec "$llng" grep -Fq '不能包含用户名或姓名' "$catalog"
}

llng_login() {
  local password=$1 expected=$2 result
  rm -f "$cookie_jar"
  curl_llng -o "$body" "$portal_url"
  curl_llng -L -D "$headers" -o "$body" -H 'Accept: application/json' \
    --data-urlencode "user=$policy_user" --data-urlencode "password=$password" "$portal_url"
  result=$(jq -r '.error // empty' "$body" 2>/dev/null || true)
  if [ "$expected" = success ]; then
    [ "$result" = 0 ]
    grep -q 'lemonldap' "$cookie_jar"
  else
    [ "$result" != 0 ]
  fi
}

llng_change() {
  local old=$1 new=$2 repeat=$3 expected=$4 result
  curl_llng -D "$headers" -o "$body" -H 'Accept: application/json' \
    --data-urlencode "oldpassword=$old" --data-urlencode "newpassword=$new" \
    --data-urlencode "confirmpassword=$repeat" "$portal_url"
  result=$(jq -r '.error // empty' "$body")
  case ",$expected," in
    *",$result,"*) ;;
    *) printf 'expected LLNG error one of [%s], got %s: ' "$expected" "$result" >&2; cat "$body" >&2; return 1 ;;
  esac
}

llng_forced_change() {
  local form_query
  rm -f "$cookie_jar"
  curl_llng -o "$body" "$portal_url"
  curl_llng -L -o "$body" -H 'Content-Type: application/x-www-form-urlencoded' \
    --data-urlencode "user=$policy_user" --data-urlencode "password=$forced_password" "$portal_url"
  grep -Eq 'trmsg="25"|newpassword' "$body"
  grep -q 'passwordPolicyMinSize' "$body"
  form_query=$(python3 - "$body" <<'PY'
import html.parser
import sys
import urllib.parse

class Form(html.parser.HTMLParser):
    def __init__(self):
        super().__init__()
        self.in_form = False
        self.values = []
    def handle_starttag(self, tag, attrs):
        item = dict(attrs)
        if tag == "form":
            self.in_form = True
        elif tag == "input" and self.in_form and item.get("name"):
            if item["name"] not in {"newpassword", "confirmpassword"}:
                self.values.append((item["name"], item.get("value", "")))
    def handle_endtag(self, tag):
        if tag == "form" and self.in_form:
            self.in_form = False

p = Form()
with open(sys.argv[1], encoding="utf-8") as stream:
    p.feed(stream.read())
print(urllib.parse.urlencode(p.values))
PY
  )
  curl_llng -o "$body" -H 'Accept: application/json' \
    --data "$form_query" --data-urlencode "newpassword=$forced_final_password" \
    --data-urlencode "confirmpassword=$forced_final_password" "$portal_url"
  [ "$(jq -r '.error // empty' "$body")" = 35 ]
}

prepare_password_policy_fixture
verify_llng_policy
allow_rapid_password_changes

printf '\n== LLNG local preflight: minimum length ==\n'
llng_login "$initial_password" success
llng_change "$initial_password" "$too_short_password" "$too_short_password" 29

printf '\n== LLNG local preflight: matching confirmation ==\n'
llng_change "$initial_password" "$changed_password" "$final_password" 34

printf '\n== LLNG Samba-final complexity rejection ==\n'
llng_change "$initial_password" "$complexity_password" "$complexity_password" 26,28

printf '\n== LLNG successful Samba writeback ==\n'
llng_change "$initial_password" "$changed_password" "$changed_password" 35
llng_login "$initial_password" failure
llng_login "$changed_password" success

printf '\n== LLNG Samba-final history rejection and coarse error mapping ==\n'
llng_change "$changed_password" "$initial_password" "$initial_password" 26,28
llng_login "$changed_password" success

printf '\n== LLNG forced-first-login guidance and password change ==\n'
dc_exec samba-tool user setpassword "$policy_user" --newpassword="$forced_password" --must-change-at-next-login >/dev/null
llng_forced_change
llng_login "$forced_password" failure
llng_login "$forced_final_password" success

printf 'PASS: LLNG Samba password-policy E2E user=%s min_length=%s history=%s min_age=%s\n' \
  "$policy_user" "$policy_min_length" "$policy_history" "$policy_min_age"
