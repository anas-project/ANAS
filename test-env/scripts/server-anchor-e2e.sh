#!/usr/bin/env sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/server-require-isolated-docker.sh"

docker_cmd=${DOCKER_CMD:-docker}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_test_}
timeout=${ANCHOR_E2E_TIMEOUT:-90}

dc="${prefix}samba_dc"
worker="${prefix}samba_dc_anchor"
suffix=$(date +%H%M%S)
event_user="aeu${suffix}"
scan_user="aer${suffix}"
group="aeg${suffix}"
computer="aec${suffix}"
password="Anas-${suffix}-E2e!"

section() {
  printf '\n== %s ==\n' "$1"
}

dc_exec() {
  "$docker_cmd" exec "$dc" "$@"
}

worker_exec() {
  "$docker_cmd" exec "$worker" "$@"
}

ldap_add_user() {
  dc_exec bash -lc \
    'exec samba-tool user add "$1" "$2" --userou="OU=People" -H ldap://127.0.0.1 -U "${SAMBA_DC_ADMIN_NAME}%${SAMBA_DC_ADMIN_PASSWORD}"' \
    anchor-e2e "$1" "$password"
}

ldap_add_group() {
  dc_exec bash -lc \
    'exec samba-tool group add "$1" --groupou="OU=Groups" -H ldap://127.0.0.1 -U "${SAMBA_DC_ADMIN_NAME}%${SAMBA_DC_ADMIN_PASSWORD}"' \
    anchor-e2e "$1"
}

ldap_add_computer() {
  dc_exec bash -lc \
    'exec samba-tool computer add "$1" -H ldap://127.0.0.1 -U "${SAMBA_DC_ADMIN_NAME}%${SAMBA_DC_ADMIN_PASSWORD}"' \
    anchor-e2e "$1"
}

cleanup() {
  "$docker_cmd" start "$worker" >/dev/null 2>&1 || true
  dc_exec samba-tool user delete "$event_user" >/dev/null 2>&1 || true
  dc_exec samba-tool user delete "$scan_user" >/dev/null 2>&1 || true
  dc_exec samba-tool group delete "$group" >/dev/null 2>&1 || true
  dc_exec samba-tool computer delete "$computer" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

wait_healthy() {
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    state=$("$docker_cmd" inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$worker")
    if [ "$state" = healthy ]; then
      return 0
    fi
    sleep 2
  done
  "$docker_cmd" logs --tail 100 "$worker" >&2 || true
  echo "anchor worker did not become healthy within ${timeout}s" >&2
  return 1
}

entry_dn() {
  kind=$1
  name=$2
  dc_exec samba-tool "$kind" show "$name" --attributes=distinguishedName \
    | sed -n 's/^distinguishedName: //p'
}

anchor_state() {
  dn=$1
  "$docker_cmd" exec -i "$worker" python3 - "$dn" <<'PY'
import os
import sys
import uuid

import ldap

dn = sys.argv[1]
ldap.set_option(ldap.OPT_X_TLS_CACERTFILE, os.environ["ANCHOR_TLS_CACERT"])
ldap.set_option(ldap.OPT_X_TLS_REQUIRE_CERT, ldap.OPT_X_TLS_DEMAND)
connection = ldap.initialize(os.environ["ANCHOR_LDAP_URL"])
connection.set_option(ldap.OPT_PROTOCOL_VERSION, 3)
connection.simple_bind_s(os.environ["ANCHOR_BIND_DN"], os.environ["ANCHOR_BIND_PASSWORD"])
rows = connection.search_s(
    dn,
    ldap.SCOPE_BASE,
    "(objectClass=*)",
    ["objectGUID", os.environ["ANCHOR_BINARY_ATTRIBUTE"], os.environ["ANCHOR_ATTRIBUTE"]],
)
attributes = rows[0][1]
object_guid = attributes.get("objectGUID", [])
binary_anchor = attributes.get(os.environ["ANCHOR_BINARY_ATTRIBUTE"], [])
printable = attributes.get(os.environ["ANCHOR_ATTRIBUTE"], [])
if not binary_anchor and not printable:
    print("missing")
elif (
    len(object_guid) == 1
    and len(binary_anchor) == 1
    and object_guid[0] == binary_anchor[0]
    and len(binary_anchor[0]) == 16
    and printable == [str(uuid.UUID(bytes_le=binary_anchor[0])).encode("ascii")]
):
    print("equal")
else:
    print("mismatch")
PY
}

wait_anchor_equal() {
  dn=$1
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    state=$(anchor_state "$dn" 2>/dev/null || true)
    if [ "$state" = equal ]; then
      return 0
    fi
    if [ "$state" = mismatch ]; then
      echo "$dn has an anchor that differs from objectGUID" >&2
      return 1
    fi
    sleep 1
  done
  "$docker_cmd" logs --tail 100 "$worker" >&2 || true
  echo "$dn was not stamped within ${timeout}s" >&2
  return 1
}

section "preflight"
dc_state=$("$docker_cmd" inspect --format '{{.State.Status}}' "$dc")
worker_state=$("$docker_cmd" inspect --format '{{.State.Status}}' "$worker")
test "$dc_state" = running
test "$worker_state" = running
wait_healthy
dc_exec grep -Fq 'dsdb_json_audit:5@/var/log/samba-audit/dsdb.json' /etc/samba/smb.conf
# Samba caps the audit log itself. A zero "max log size" would not merely
# uncap it -- it short-circuits the check that reopens the file after a
# rotation, which is what keeps every Samba process writing to the file the
# anchor worker is reading. testparm reports the effective value.
audit_max_log_size=$(dc_exec testparm -s --parameter-name='max log size' 2>/dev/null | awk 'NF { value = $NF } END { print value }')
test "$audit_max_log_size" -gt 0
# Nothing consumes the transaction audit, so it must not be writing a file.
dc_exec test ! -e /var/log/samba-audit/transaction.json
dc_exec test -f /run/anas-identity-schema.ready
dc_exec ldbsearch -H /var/lib/samba/private/sam.ldb \
  -b "CN=Schema,CN=Configuration,$(dc_exec printenv SAMBA_DC_BASE_DN)" \
  '(lDAPDisplayName=anasIdentityAnchor)' attributeID \
  | awk '/^ / { line = line substr($0, 2); next } { if (line != "") print line; line = $0 } END { if (line != "") print line }' \
  | grep -Fq '1.3.6.1.4.1.66678.1.2.1'
printf 'dc=%s worker=%s\n' "$dc_state" "$worker_state"

section "audit-triggered user stamping"
ldap_add_user "$event_user"
event_user_dn=$(entry_dn user "$event_user")
test -n "$event_user_dn"
wait_anchor_equal "$event_user_dn"
printf 'user=%s anchor=equal\n' "$event_user_dn"

section "audit-triggered group stamping"
ldap_add_group "$group"
group_dn=$(entry_dn group "$group")
test -n "$group_dn"
wait_anchor_equal "$group_dn"
printf 'group=%s anchor=equal\n' "$group_dn"

section "excluded computer remains untouched"
ldap_add_computer "$computer"
computer_dn=$(entry_dn computer "$computer")
test -n "$computer_dn"
sleep 3
computer_state=$(anchor_state "$computer_dn")
test "$computer_state" = missing
printf 'computer=%s anchor=missing\n' "$computer_dn"

section "startup reconciliation recovers a missed event"
"$docker_cmd" stop "$worker" >/dev/null
dc_exec samba-tool user add "$scan_user" "$password" --userou='OU=People'
scan_user_dn=$(entry_dn user "$scan_user")
test -n "$scan_user_dn"
"$docker_cmd" start "$worker" >/dev/null
wait_healthy
wait_anchor_equal "$scan_user_dn"
printf 'user=%s anchor=equal source=reconciliation\n' "$scan_user_dn"

section "audit evidence and final health"
dc_exec grep -F "$event_user_dn" /var/log/samba-audit/dsdb.json >/dev/null
dc_exec grep -F "$group_dn" /var/log/samba-audit/dsdb.json >/dev/null
worker_exec python3 /app/anchor_worker.py --healthcheck
"$docker_cmd" inspect --format '{{json .State.Health}}' "$worker"

printf '\nidentity anchor e2e tests passed\n'
