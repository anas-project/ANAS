#!/usr/bin/env bash
# Dynamic DNS end to end against a real vendor.
#
# Everything else in the DDNS work can be tested without leaving the machine.
# This cannot: whether the registry's credential mapping actually authenticates,
# and whether the records the deployment declares actually appear, is only
# answerable by asking the vendor.
#
# The target is the test host's own name, so the run snapshots the records
# first and restores them on any failure.
set -euo pipefail

socket=${ANAS_TEST_DOCKER_SOCKET:-/run/anas-docker-test.sock}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_ddnse2e_}
domain=${ANAS_TEST_DOMAIN:-ln.hlong.wang}
zone=${ANAS_TEST_ZONE:-hlong.wang}
subdomain=${domain%%.$zone}
credentials=${ANAS_TEST_CREDENTIALS:-$HOME/.anas-ddns-e2e.env}
# The updater polls on this interval; allow a few cycles plus API latency.
deadline=${ANAS_TEST_DEADLINE:-240}

# shellcheck source=/dev/null
[ -f "$credentials" ] && . "$credentials"
: "${TENCENTCLOUD_SECRET_ID:?set TENCENTCLOUD_SECRET_ID, or point ANAS_TEST_CREDENTIALS at a file that does}"
: "${TENCENTCLOUD_SECRET_KEY:?set TENCENTCLOUD_SECRET_KEY}"

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
workdir=$(mktemp -d)
restore_needed=0
trap cleanup EXIT

say() { printf '\n== %s ==\n' "$1"; }

# dnsapi calls the vendor. Signing lives in a helper rather than in this script
# because TC3 chains four HMACs and getting that wrong in shell is a poor use
# of anyone's afternoon.
dnsapi() {
  TENCENTCLOUD_SECRET_ID="$TENCENTCLOUD_SECRET_ID" \
  TENCENTCLOUD_SECRET_KEY="$TENCENTCLOUD_SECRET_KEY" \
    go run "$root/test-env/scripts/tencentdns" "$1" "$2"
}

record_value() {
  dnsapi DescribeRecordList "{\"Domain\":\"$zone\",\"Subdomain\":\"$1\",\"RecordType\":\"$2\"}" |
    python3 -c '
import json, sys
data = json.load(sys.stdin).get("Response", {})
records = data.get("RecordList") or []
print(records[0]["Value"] if records else "")
'
}

cleanup() {
  status=$?
  if [ "$status" -ne 0 ] && [ "$restore_needed" -eq 1 ]; then
    printf '\nrun failed; restoring the records this test changed\n' >&2
    restore_records || printf 'could not restore records automatically\n' >&2
  fi
  "$compose" down --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$workdir"
  return "$status"
}

restore_records() {
  for entry in "A:$before_a" "AAAA:$before_aaaa"; do
    type=${entry%%:*}
    value=${entry#*:}
    [ -n "$value" ] || continue
    current=$(record_value "$subdomain" "$type")
    if [ "$current" != "$value" ]; then
      printf 'restoring %s %s -> %s\n' "$type" "$domain" "$value" >&2
      dnsapi ModifyRecord \
        "{\"Domain\":\"$zone\",\"SubDomain\":\"$subdomain\",\"RecordType\":\"$type\",\"RecordLine\":\"默认\",\"Value\":\"$value\",\"RecordId\":$(record_id "$subdomain" "$type")}" >/dev/null
    fi
  done
}

record_id() {
  dnsapi DescribeRecordList "{\"Domain\":\"$zone\",\"Subdomain\":\"$1\",\"RecordType\":\"$2\"}" |
    python3 -c '
import json, sys
records = json.load(sys.stdin).get("Response", {}).get("RecordList") or []
print(records[0]["RecordId"] if records else 0)
'
}

say "credentials and zone"
dnsapi DescribeDomain "{\"Domain\":\"$zone\"}" |
  python3 -c '
import json, sys
info = json.load(sys.stdin)["Response"]["DomainInfo"]
print(f"zone {info[\"Domain\"]} id={info[\"DomainId\"]} records={info[\"RecordCount\"]}")
'

say "snapshot the records this test will touch"
before_a=$(record_value "$subdomain" A)
before_aaaa=$(record_value "$subdomain" AAAA)
before_wildcard_a=$(record_value "*.$subdomain" A)
printf 'A     %s = %s\n' "$domain" "${before_a:-<absent>}"
printf 'AAAA  %s = %s\n' "$domain" "${before_aaaa:-<absent>}"
printf 'A     *.%s = %s\n' "$domain" "${before_wildcard_a:-<absent>}"

say "what the host actually is"
host_v4=$(curl -4 -s --max-time 10 https://myip.ipip.net | grep -oE '[0-9]+(\.[0-9]+){3}' | head -1 || true)
host_v6=$(ip -6 addr show scope global 2>/dev/null |
  awk '/inet6/ { print $2 }' | cut -d/ -f1 | grep -v '^f[cd]' | head -1 || true)
printf 'egress IPv4  %s\n' "${host_v4:-<none>}"
printf 'global IPv6  %s\n' "${host_v6:-<none>}"
[ -n "$host_v4" ] || { echo "cannot determine this host's public IPv4" >&2; exit 1; }

say "render and start"
workspace=${ANAS_TEST_WORKSPACE:-$HOME/anas-ddns-e2e}
mkdir -p "$workspace"
[ -d "$workspace/.anas" ] || (cd "$root" && go run ./cmd/anas init "$workspace" -y >/dev/null)
cp "$root/test-env/server-ddns-e2e.yml" "$workspace/config.yml"
{
  echo "secrets:"
  echo "  tencentcloud_secret_id: $TENCENTCLOUD_SECRET_ID"
  echo "  tencentcloud_secret_key: $TENCENTCLOUD_SECRET_KEY"
} >> "$workspace/config.yml"
chmod 600 "$workspace/config.yml"

(cd "$root" && go run ./cmd/anas plan -c "$workspace/config.yml")

say "the capability resolved without naming a module"
plan=$(cd "$root" && go run ./cmd/anas plan -c "$workspace/config.yml")
printf '%s\n' "$plan" | grep -q '^ddns_go$' ||
  { echo "ddns_go was not pulled in by the capability" >&2; exit 1; }
if printf '%s\n' "$plan" | grep -q '^oauth2_proxy$'; then
  echo "oauth2_proxy was unexpectedly pulled in by ddns_go" >&2
  exit 1
fi
printf '%s\n' "$plan" | grep -q 'dynamic dns: ddns_go (auto)' ||
  { echo "the plan does not report the resolved implementation" >&2; exit 1; }

(cd "$root" && go run ./cmd/anas apply -w "$workspace" --build)
restore_needed=1

deployment=$(ls -d "$workspace"/.anas/deployments/*/ | tail -1)
compose="docker -H unix://$socket compose -f ${deployment}modules/ddns_go/docker-compose.yml --env-file ${deployment}modules/ddns_go/.env"

say "the rendered configuration reached the container"
docker -H "unix://$socket" exec "${prefix}ddns_go" \
  sh -c 'ls -l /root/.ddns_go_config.yaml' | grep -q '^-rw-------' ||
  { echo "the merged config is not 0600" >&2; exit 1; }
docker -H "unix://$socket" exec "${prefix}ddns_go" \
  grep -q 'anas-managed:primary' /root/.ddns_go_config.yaml ||
  { echo "the managed entry is missing from the merged config" >&2; exit 1; }

say "wait for the records to converge"
waited=0
while [ "$waited" -lt "$deadline" ]; do
  now_a=$(record_value "$subdomain" A)
  now_wildcard=$(record_value "*.$subdomain" A)
  if [ "$now_a" = "$host_v4" ] && [ "$now_wildcard" = "$host_v4" ]; then
    break
  fi
  sleep 10
  waited=$((waited + 10))
  printf 'waited %ss: A=%s wildcard=%s want=%s\n' "$waited" "${now_a:-<absent>}" "${now_wildcard:-<absent>}" "$host_v4"
done

say "verify at the vendor"
final_a=$(record_value "$subdomain" A)
final_wildcard=$(record_value "*.$subdomain" A)
[ "$final_a" = "$host_v4" ] ||
  { echo "A $domain = ${final_a:-<absent>}, want $host_v4" >&2; exit 1; }
# The wildcard did not exist before this run, so its appearance is the
# unambiguous proof that the credential mapping works end to end.
[ "$final_wildcard" = "$host_v4" ] ||
  { echo "A *.$domain = ${final_wildcard:-<absent>}, want $host_v4" >&2; exit 1; }
printf 'A     %s = %s\n' "$domain" "$final_a"
printf 'A     *.%s = %s\n' "$domain" "$final_wildcard"

if [ -n "$host_v6" ]; then
  final_aaaa=$(record_value "$subdomain" AAAA)
  [ "$final_aaaa" = "$host_v6" ] ||
    { echo "AAAA $domain = ${final_aaaa:-<absent>}, want $host_v6" >&2; exit 1; }
  printf 'AAAA  %s = %s\n' "$domain" "$final_aaaa"
fi

say "verify at an authoritative nameserver"
authoritative=$(dig +short "$zone" NS | head -1)
resolved=$(dig +short "@$authoritative" "$domain" A | tail -1)
[ "$resolved" = "$host_v4" ] ||
  { echo "$authoritative answers $resolved for $domain, want $host_v4" >&2; exit 1; }
printf '%s answers %s for %s\n' "$authoritative" "$resolved" "$domain"

say "the managed local account is the only Web authentication"
credential=$(cd "$root" && go run ./cmd/anas admin local credential ddns_go -w "$workspace" --json)
local_username=$(CREDENTIAL_JSON=$credential python3 -c 'import json,os; print(json.loads(os.environ["CREDENTIAL_JSON"])["account"]["username"])')
local_password=$(CREDENTIAL_JSON=$credential python3 -c 'import json,os; print(json.loads(os.environ["CREDENTIAL_JSON"])["account"]["password"])')
cookie_jar=$(mktemp)

# Direct host-port access bypasses Traefik by construction, so this proves the
# native account itself is effective rather than merely present in YAML.
direct_code=$(curl -s --max-time 10 -o /dev/null -w '%{http_code}' "http://127.0.0.1:9876/" || true)
case "$direct_code" in 302|303|307) ;; *)
  echo "unauthenticated direct request returned $direct_code, want a login redirect" >&2; exit 1 ;;
esac
curl -sS --max-time 10 -c "$cookie_jar" -o /dev/null -H 'Content-Type: application/json' \
  --data "{\"Username\":\"$local_username\",\"Password\":\"$local_password\"}" \
  "http://127.0.0.1:9876/loginFunc"
grep -q '[[:space:]]token[[:space:]]' "$cookie_jar" ||
  { echo "managed local credential did not receive a login cookie" >&2; exit 1; }
curl -sS --max-time 10 -b "$cookie_jar" -o /dev/null -w '%{http_code}' \
  "http://127.0.0.1:9876/" | grep -q '^200$' ||
  { echo "authenticated direct request did not reach ddns-go" >&2; exit 1; }
rm -f "$cookie_jar"

say "rotate the native account transactionally"
(cd "$root" && go run ./cmd/anas admin local rotate ddns_go -w "$workspace")
rotated_credential=$(cd "$root" && go run ./cmd/anas admin local credential ddns_go -w "$workspace" --json)
rotated_password=$(CREDENTIAL_JSON=$rotated_credential python3 -c 'import json,os; print(json.loads(os.environ["CREDENTIAL_JSON"])["account"]["password"])')
[ "$rotated_password" != "$local_password" ] ||
  { echo "rotation did not change the generated password" >&2; exit 1; }

old_cookie=$(mktemp)
curl -sS --max-time 10 -c "$old_cookie" -o /dev/null -H 'Content-Type: application/json' \
  --data "{\"Username\":\"$local_username\",\"Password\":\"$local_password\"}" \
  "http://127.0.0.1:9876/loginFunc"
if grep -q '[[:space:]]token[[:space:]]' "$old_cookie"; then
  echo "old password still authenticates after rotation" >&2
  exit 1
fi
rm -f "$old_cookie"

new_cookie=$(mktemp)
curl -sS --max-time 10 -c "$new_cookie" -o /dev/null -H 'Content-Type: application/json' \
  --data "{\"Username\":\"$local_username\",\"Password\":\"$rotated_password\"}" \
  "http://127.0.0.1:9876/loginFunc"
grep -q '[[:space:]]token[[:space:]]' "$new_cookie" ||
  { echo "rotated password did not authenticate" >&2; exit 1; }
rm -f "$new_cookie"

# Through Traefik, an unauthenticated request must reach ddns-go's own login,
# not an oauth2-proxy redirect.
entry_port=${ANAS_TEST_ENTRY_PORT:-9443}
headers=$(mktemp)
code=$(curl -sk --max-time 10 -D "$headers" -o /dev/null -w '%{http_code}' \
  --resolve "ddns-go.$domain:$entry_port:127.0.0.1" \
  "https://ddns-go.$domain:$entry_port/" || true)
case "$code" in
  302|303|307)
    grep -i '^location: /login' "$headers" >/dev/null ||
      { echo "domain entry did not redirect to ddns-go's native login" >&2; exit 1; }
    printf 'unauthenticated domain request reached the native login (%s)\n' "$code" ;;
  200)
    echo "unauthenticated request reached the interface (HTTP 200)" >&2; exit 1 ;;
  *)
    printf 'unauthenticated request returned %s\n' "$code" ;;
esac
rm -f "$headers"

say "restore"
restore_records
restore_needed=0
printf '\nthe wildcard record *.%s was created by this run and is left in place;\n' "$domain"
printf 'delete it at the vendor if it is not wanted.\n'

say "passed"
