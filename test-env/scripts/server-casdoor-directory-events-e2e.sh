#!/usr/bin/env bash
# Casdoor directory-event subscription runtime test.
#
# This is intentionally a server-only E2E. It proves that Casdoor remains
# stale while its subscriber is stopped, then imports Samba changes promptly
# after the durable journal subscriber resumes. It also covers attribute
# refresh, burst debouncing, cursor durability, and sidecar health.
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"

prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_casdoor_}
timeout=${CASDOOR_DIRECTORY_EVENTS_E2E_TIMEOUT:-180}

dc="${prefix}samba_dc"
anchor="${prefix}samba_dc_anchor"
casdoor="${prefix}casdoor"
dirwatch="${prefix}casdoor_dirwatch"

suffix=$(date +%H%M%S)
user="cdu${suffix}"
extra_users=("cda${suffix}" "cdb${suffix}" "cdc${suffix}")
password="Anas-${suffix}-E2e!"
journal=/var/lib/anas-directory-events/events.jsonl

section() { printf '\n== %s ==\n' "$1"; }

dc_exec() { docker exec "$dc" "$@"; }
dc_exec_stdin() { docker exec -i "$dc" "$@"; }

ldap_modify() {
  dc_exec_stdin bash -lc \
    'exec ldbmodify -H ldap://127.0.0.1 -U "${SAMBA_DC_ADMIN_NAME}%${SAMBA_DC_ADMIN_PASSWORD}"'
}

samba_tool() {
  dc_exec bash -lc \
    'exec samba-tool "$@" -H ldap://127.0.0.1 -U "${SAMBA_DC_ADMIN_NAME}%${SAMBA_DC_ADMIN_PASSWORD}"' \
    casdoor-directory-events-e2e "$@"
}

entry_dn() {
  dc_exec bash -lc \
    'exec samba-tool "$1" show "$2" --attributes=distinguishedName -H ldap://127.0.0.1 -U "${SAMBA_DC_ADMIN_NAME}%${SAMBA_DC_ADMIN_PASSWORD}"' \
    casdoor-directory-events-e2e "$1" "$2" | sed -n 's/^distinguishedName: //p'
}

cleanup() {
  docker start "$dirwatch" >/dev/null 2>&1 || true
  for name in "$user" "${extra_users[@]}"; do
    samba_tool user delete "$name" >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT HUP INT TERM

journal_seq() {
  docker exec "$anchor" sh -c "tail -n 1 $journal 2>/dev/null || true" \
    | sed -n 's/.*"seq": *\([0-9]*\).*/\1/p' || true
}

journal_since() {
  docker exec "$anchor" sh -c "cat $journal 2>/dev/null || true" \
    | awk -v since="$1" '{ if (match($0, /"seq": *[0-9]+/)) { s = substr($0, RSTART+7, RLENGTH-7) + 0; if (s > since) print } }' || true
}

# Invoke Casdoor's authenticated API through the shipped helper. The helper
# reads its managed client credential from the container environment, so the
# secret never enters this script's argv.
casdoor_user_field() {
  docker exec "$dirwatch" /opt/anas/bin/casdoor-helper directory-watch --get-user "anas/$1" 2>/dev/null \
    | jq -r --arg field "$2" '.[$field] // empty' || true
}

watcher_health() {
  docker exec "$dirwatch" cat /data/anas-dirwatch/health.json
}

trigger_count() {
  watcher_health | jq -r '.trigger_count // 0'
}

wait_for_user() {
  local name=$1 deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    [ "$(casdoor_user_field "$name" name)" = "$name" ] && return 0
    sleep 5
  done
  docker logs --tail 80 "$dirwatch" >&2 || true
  printf 'Casdoor did not import %s within %ss\n' "$name" "$timeout" >&2
  return 1
}

wait_for_field() {
  local name=$1 field=$2 expected=$3 deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    [ "$(casdoor_user_field "$name" "$field")" = "$expected" ] && return 0
    sleep 5
  done
  docker logs --tail 80 "$dirwatch" >&2 || true
  printf 'Casdoor did not update %s.%s to %s within %ss\n' "$name" "$field" "$expected" "$timeout" >&2
  return 1
}

section "preflight"
for container in "$dc" "$anchor" "$casdoor" "$dirwatch"; do
  test "$(docker inspect --format '{{.State.Status}}' "$container")" = running
done
docker exec "$dirwatch" /opt/anas/bin/casdoor-helper directory-watch --healthcheck
test "$(docker exec "$dirwatch" printenv CASDOOR_LDAP_AUTO_SYNC_MINUTES)" = 1440
watcher_health | jq -e '.ready == true' >/dev/null
printf 'Casdoor and its directory subscriber are healthy\n'

section "STALE SYNC: stopping the subscriber leaves a new Samba account absent"
docker stop "$dirwatch" >/dev/null
before=$(journal_seq); before=${before:-0}
samba_tool user add "$user" "$password" --userou="OU=People" >/dev/null
deadline=$(( $(date +%s) + 30 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  journal_since "$before" | grep -Fq "CN=$user," && break
  sleep 2
done
journal_since "$before" | grep -Fq "CN=$user,"
sleep 15
test -z "$(casdoor_user_field "$user" name)"
printf 'Samba and the journal contain %s while Casdoor remains stale\n' "$user"

section "the subscriber imports the pending account"
docker start "$dirwatch" >/dev/null
wait_for_user "$user"
printf 'Casdoor imported %s without waiting for its daily fallback\n' "$user"

section "a watched attribute modification is synchronized"
user_dn=$(entry_dn user "$user")
test -n "$user_dn"
display_name="Casdoor Event ${suffix}"
printf 'dn: %s\nchangetype: modify\nreplace: displayName\ndisplayName: %s\n' \
  "$user_dn" "$display_name" | ldap_modify >/dev/null
wait_for_field "$user" displayName "$display_name"
printf 'Casdoor refreshed displayName through the event path\n'

section "a burst collapses into one full LDAP synchronization"
sleep "$(docker exec "$dirwatch" printenv CASDOOR_DIRWATCH_MIN_INTERVAL_SECONDS)"
count_before=$(trigger_count)
for name in "${extra_users[@]}"; do
  samba_tool user add "$name" "$password" --userou="OU=People" >/dev/null
done
for name in "${extra_users[@]}"; do
  wait_for_user "$name"
done
count_after=$(trigger_count)
test "$count_after" -gt "$count_before"
test $(( count_after - count_before )) -le 1
printf 'three creates caused %d synchronization\n' $(( count_after - count_before ))

section "a restart preserves the cursor and does not replay"
docker restart "$dirwatch" >/dev/null
sleep 20
docker exec "$dirwatch" /opt/anas/bin/casdoor-helper directory-watch --healthcheck
test "$(trigger_count)" = 0
printf 'the persisted cursor suppressed replay after restart\n'

printf '\nCasdoor directory event subscription E2E tests passed\n'
