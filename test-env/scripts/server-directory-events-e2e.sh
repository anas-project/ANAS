#!/usr/bin/env bash
# Directory event journal runtime test.
#
# The failure this guards against is not a crash. authentik verifies passwords
# with an LDAP bind but reads groups from its last sync, so a user added to an
# application group authenticates successfully and is still denied the
# application until the next scheduled run. This reproduces that window
# deliberately, then shows the journal closing it.
set -euo pipefail

prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_anchor_}
timeout=${DIRECTORY_EVENTS_E2E_TIMEOUT:-180}

dc="${prefix}samba_dc"
anchor="${prefix}samba_dc_anchor"
authentik="${prefix}authentik"
dirwatch="${prefix}authentik_dirwatch"

suffix=$(date +%H%M%S)
user="deu${suffix}"
extra_users=("dea${suffix}" "deb${suffix}" "dec${suffix}")
group="APP_deg${suffix}"
password="Anas-${suffix}-E2e!"
journal=/var/lib/anas-directory-events/events.jsonl

section() { printf '\n== %s ==\n' "$1"; }

dc_exec() { docker exec "$dc" "$@"; }
dc_exec_stdin() { docker exec -i "$dc" "$@"; }

entry_dn() {
  dc_exec bash -lc \
    'exec samba-tool "$1" show "$2" --attributes=distinguishedName -H ldap://127.0.0.1 -U "${SAMBA_DC_ADMIN_NAME}%${SAMBA_DC_ADMIN_PASSWORD}"' \
    directory-events-e2e "$1" "$2" | sed -n 's/^distinguishedName: //p'
}

samba_tool() {
  dc_exec bash -lc \
    'exec samba-tool "$@" -H ldap://127.0.0.1 -U "${SAMBA_DC_ADMIN_NAME}%${SAMBA_DC_ADMIN_PASSWORD}"' \
    directory-events-e2e "$@"
}

cleanup() {
  docker start "$dirwatch" >/dev/null 2>&1 || true
  for name in "$user" "${extra_users[@]}"; do
    samba_tool user delete "$name" >/dev/null 2>&1 || true
  done
  samba_tool group delete "$group" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

# Read the deployed debounce settings rather than assuming them, so the waits
# stay correct if the defaults are retuned.
container_env() {
  docker exec "$1" printenv "$2" 2>/dev/null || true
}

# The journal is created lazily on the first published event, so both helpers
# have to read an absent file as "nothing yet" rather than as a failure --
# under pipefail a missing file would otherwise abort the run.
journal_seq() {
  docker exec "$anchor" sh -c "tail -n 1 $journal 2>/dev/null || true" \
    | sed -n 's/.*"seq": *\([0-9]*\).*/\1/p' || true
}

journal_since() {
  docker exec "$anchor" sh -c "cat $journal 2>/dev/null || true" \
    | awk -v since="$1" '{ if (match($0, /"seq": *[0-9]+/)) { s = substr($0, RSTART+7, RLENGTH-7) + 0; if (s > since) print } }' || true
}

# Stamping an identity anchor is itself a directory write, so a create keeps
# producing journal entries for a moment after samba-tool returns. Anything
# asserting "nothing was published" has to start from a quiet journal.
journal_quiesce() {
  local last="" current deadline=$(( $(date +%s) + 60 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    current=$(journal_seq)
    if [ -n "$last" ] && [ "$current" = "$last" ]; then
      return 0
    fi
    last=$current
    sleep 4
  done
  return 0
}

ak_shell() {
  docker exec "$authentik" ak shell -c "$1" 2>/dev/null | sed -n 's/^E2E>>> //p'
}

authentik_groups() {
  ak_shell "
from authentik.core.models import User
u = User.objects.filter(username='$1').first()
print('E2E>>> ' + (','.join(sorted(g.name for g in u.all_groups())) if u else '<absent>'))
"
}

sync_count() {
  ak_shell "
from authentik.tasks.models import Task
print('E2E>>> %d' % Task.objects.filter(actor_name='authentik.sources.ldap.tasks.ldap_sync').count())
"
}

wait_for_group() {
  local want=$1 deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    case ",$(authentik_groups "$user")," in
      *",$want,"*) return 0 ;;
    esac
    sleep 5
  done
  docker logs --tail 50 "$dirwatch" >&2 || true
  echo "authentik did not learn $user is in $want within ${timeout}s" >&2
  return 1
}

wait_for_user_known() {
  local deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    [ "$(authentik_groups "$user")" != "<absent>" ] && return 0
    sleep 5
  done
  docker logs --tail 50 "$dirwatch" >&2 || true
  echo "authentik never saw $user within ${timeout}s" >&2
  return 1
}

section "preflight"
for container in "$dc" "$anchor" "$authentik" "$dirwatch"; do
  test "$(docker inspect --format '{{.State.Status}}' "$container")" = running
done

# The watcher runs as a bare script rather than through `ak`, which puts its
# own directory on sys.path instead of the working directory: the authentik
# package then is not importable and the container crash-loops. Nothing in the
# unit tests catches that, because they never import Django. A restart count
# that keeps climbing is the signature.
restarts=$(docker inspect --format '{{.RestartCount}}' "$dirwatch")
sleep 10
test "$(docker inspect --format '{{.RestartCount}}' "$dirwatch")" = "$restarts"
docker logs --tail 50 "$dirwatch" 2>&1 | grep -q "ModuleNotFoundError" && {
  echo "the watcher cannot import its own runtime" >&2
  docker logs --tail 20 "$dirwatch" >&2
  exit 1
}
printf 'watcher is stable at %s restarts\n' "$restarts"

# Samba's audit stream has to reach the anchor worker, and the worker has to be
# able to publish. Both are bind mounts owned by the host, so an ownership slip
# leaves the worker unable to write the journal at all.
docker exec "$dc" test -f /var/log/samba-audit/dsdb.json
docker exec "$anchor" test -r /var/log/samba-audit/dsdb.json
docker exec "$anchor" sh -c "test -w $(dirname $journal)"
printf 'audit stream readable, journal directory writable\n'
debounce=$(container_env "$dirwatch" AUTHENTIK_DIRWATCH_DEBOUNCE_SECONDS)
min_interval=$(container_env "$dirwatch" AUTHENTIK_DIRWATCH_MIN_INTERVAL_SECONDS)
test -n "$debounce" && test -n "$min_interval"
docker exec "$dirwatch" python3 /app/directory_watch.py --healthcheck
printf 'debounce=%ss min_interval=%ss\n' "$debounce" "$min_interval"

section "an in-scope create reaches the journal"
before=$(journal_seq); before=${before:-0}
samba_tool user add "$user" "$password" --userou="OU=People"
samba_tool group add "$group" --groupou="OU=Apps,OU=Groups"
deadline=$(( $(date +%s) + 30 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  journal_since "$before" | grep -Fq "CN=$user," && break
  sleep 2
done
journal_since "$before" | grep -Fq "CN=$user,"
printf 'journal published the create of %s\n' "$user"

section "an unwatched attribute is not published"
journal_quiesce
mark=$(journal_seq)
user_dn=$(entry_dn user "$user")
test -n "$user_dn"
printf 'dn: %s\nchangetype: modify\nreplace: description\ndescription: directory-events-e2e\n' \
  "$user_dn" | dc_exec_stdin ldbmodify -H /var/lib/samba/private/sam.ldb >/dev/null
sleep 8
if journal_since "$mark" | grep -Fq "CN=$user,"; then
  journal_since "$mark" >&2
  echo "a description-only change must not wake subscribers" >&2
  exit 1
fi
printf 'description-only change stayed out of the journal\n'

section "authentik learns the new account"
wait_for_user_known
printf 'authentik sees %s\n' "$user"

section "STALE SYNC: with the watcher stopped, a group change stays invisible"
# This is the production failure of 2026-08-08 reproduced on purpose: the
# membership is correct in AD, and authentik still denies the application.
docker stop "$dirwatch" >/dev/null
syncs_before=$(sync_count)
samba_tool group addmembers "$group" "$user"
dc_exec bash -lc "samba-tool group listmembers '$group'" | grep -Fxq "$user"
sleep $(( debounce + 20 ))
stale=$(authentik_groups "$user")
case ",$stale," in
  *",$group,"*)
    echo "expected authentik to still be stale, but it already had $group" >&2
    exit 1 ;;
esac
test "$(sync_count)" = "$syncs_before"
printf 'AD has the membership, authentik does not: %s\n' "${stale:-<none>}"

section "the journal closes the window once the watcher is back"
docker start "$dirwatch" >/dev/null
wait_for_group "$group"
printf 'authentik converged on %s without waiting for the schedule\n' "$group"

section "a burst of members collapses into one sync"
sleep "$min_interval"
syncs_before=$(sync_count)
for name in "${extra_users[@]}"; do
  samba_tool user add "$name" "$password" --userou="OU=People" >/dev/null
  samba_tool group addmembers "$group" "$name" >/dev/null
done
sleep $(( debounce + 25 ))
syncs_after=$(sync_count)
test "$syncs_after" -gt "$syncs_before"
test $(( syncs_after - syncs_before )) -le 1
printf 'three members added, syncs triggered: %d\n' $(( syncs_after - syncs_before ))

section "a restart does not replay the journal"
sleep "$min_interval"
syncs_before=$(sync_count)
docker restart "$dirwatch" >/dev/null
sleep $(( debounce + 25 ))
test "$(sync_count)" = "$syncs_before"
docker exec "$dirwatch" python3 /app/directory_watch.py --healthcheck
printf 'cursor survived the restart, no replay\n'

section "the deployment survives a watcher restart cycle"
# The dirwatch container is the only service in the stack whose image is the
# authentik image driven by a mounted script, so a bad mount or a bad
# entrypoint shows up as a crash loop rather than a failed deploy.
docker restart "$dirwatch" >/dev/null
deadline=$(( $(date +%s) + 60 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  [ "$(docker inspect --format '{{.State.Status}}' "$dirwatch")" = running ] && break
  sleep 2
done
test "$(docker inspect --format '{{.State.Status}}' "$dirwatch")" = running
sleep 10
test "$(docker inspect --format '{{.State.Status}}' "$dirwatch")" = running
docker exec "$dirwatch" python3 /app/directory_watch.py --healthcheck
printf 'watcher came back and stayed up\n'

printf '\ndirectory event journal e2e tests passed\n'
