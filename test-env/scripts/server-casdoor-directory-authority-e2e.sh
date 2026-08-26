#!/usr/bin/env bash
# Casdoor directory authority reconciliation E2E.
#
# Requires a rendered Casdoor deployment with an OIDC/SAML consumer whose
# ALLOW_GROUPS includes APP_nextcloud. It checks the Casdoor shadow state; real
# protocol login and application-session assertions remain separate E2Es.
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"

docker_cmd=${DOCKER_CMD:-docker}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_casdoor_}
timeout=${CASDOOR_DIRECTORY_AUTHORITY_E2E_TIMEOUT:-240}
dc="${prefix}samba_dc"
dirwatch="${prefix}casdoor_dirwatch"

suffix=$(date +%H%M%S)
direct_user="cau${suffix}"
renamed_user="car${suffix}"
nested_user="can${suffix}"
nested_group="ROLE_casdoor_${suffix}"
password="Casdoor-${suffix}-Authority-E2e!"

section() { printf '\n== %s ==\n' "$1"; }
dc_exec() { "$docker_cmd" exec "$dc" "$@"; }

samba_tool() {
  dc_exec bash -lc \
    'exec samba-tool "$@" -H ldap://127.0.0.1 -U "${SAMBA_DC_ADMIN_NAME}%${SAMBA_DC_ADMIN_PASSWORD}"' \
    casdoor-directory-authority-e2e "$@"
}

casdoor_user() {
  "$docker_cmd" exec "$dirwatch" /opt/anas/bin/casdoor-helper directory-watch \
    --get-user "anas/$1" 2>/dev/null || printf 'null\n'
}

anchor_of() {
  samba_tool user show "$1" --attributes=anasIdentityAnchor 2>/dev/null |
    sed -n 's/^anasIdentityAnchor: //p'
}

wait_for_anchor() {
  local user=$1 deadline anchor
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    anchor=$(anchor_of "$user")
    [ -n "$anchor" ] && { printf '%s\n' "$anchor"; return 0; }
    sleep 2
  done
  printf 'Samba did not stamp an identity anchor for %s\n' "$user" >&2
  return 1
}

wait_for_user_state() {
  local user=$1 expression=$2 description=$3 deadline current
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    current=$(casdoor_user "$user")
    if printf '%s' "$current" | jq -e "$expression" >/dev/null 2>&1; then
      return 0
    fi
    sleep 5
  done
  "$docker_cmd" logs --tail 100 "$dirwatch" >&2 || true
  printf 'Casdoor did not converge %s (%s); last state: %s\n' "$user" "$description" "$current" >&2
  return 1
}

cleanup() {
  samba_tool user delete "$direct_user" >/dev/null 2>&1 || true
  samba_tool user delete "$renamed_user" >/dev/null 2>&1 || true
  samba_tool user delete "$nested_user" >/dev/null 2>&1 || true
  samba_tool group delete "$nested_group" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM
trap 'printf "FAIL: Casdoor directory authority E2E line=%s\n" "$LINENO" >&2' ERR

section "preflight"
for container in "$dc" "$dirwatch"; do
  test "$("$docker_cmd" inspect --format '{{.State.Status}}' "$container")" = running
done
"$docker_cmd" exec "$dirwatch" /opt/anas/bin/casdoor-helper directory-watch --healthcheck
case ",$("$docker_cmd" exec "$dirwatch" printenv CASDOOR_DIRWATCH_MANAGED_GROUPS)," in
  *,APP_nextcloud,*) ;;
  *) printf 'fixture must declare APP_nextcloud through a Consumer ALLOW_GROUPS\n' >&2; exit 2 ;;
esac
cleanup
printf 'Casdoor directory subscriber and managed group policy are ready\n'

section "direct and recursive membership converge"
samba_tool user add "$direct_user" "$password" --userou='OU=People' >/dev/null
samba_tool user add "$nested_user" "$password" --userou='OU=People' >/dev/null
samba_tool group add "$nested_group" --groupou='OU=Role,OU=Groups' >/dev/null
samba_tool group addmembers APP_nextcloud "$direct_user" >/dev/null
samba_tool group addmembers "$nested_group" "$nested_user" >/dev/null
samba_tool group addmembers APP_nextcloud "$nested_group" >/dev/null
direct_anchor=$(wait_for_anchor "$direct_user")
nested_anchor=$(wait_for_anchor "$nested_user")
wait_for_user_state "$direct_user" \
  ".id == \"$direct_anchor\" and (.groups | index(\"anas/APP_nextcloud\") != null)" \
  'direct group and permanent anchor'
wait_for_user_state "$nested_user" \
  ".id == \"$nested_anchor\" and (.groups | index(\"anas/APP_nextcloud\") != null)" \
  'recursive group and permanent anchor'
printf 'direct and recursive memberships use the stamped permanent anchors\n'

section "group removals are authoritative"
samba_tool group removemembers APP_nextcloud "$direct_user" >/dev/null
samba_tool group removemembers APP_nextcloud "$nested_group" >/dev/null
wait_for_user_state "$direct_user" \
  '(.groups | index("anas/APP_nextcloud")) == null' 'direct group removal'
wait_for_user_state "$nested_user" \
  '(.groups | index("anas/APP_nextcloud")) == null' 'recursive group removal'
printf 'direct and recursive access groups were revoked\n'

section "disable and re-enable converge without replacing identity"
samba_tool group addmembers APP_nextcloud "$direct_user" >/dev/null
wait_for_user_state "$direct_user" \
  '(.groups | index("anas/APP_nextcloud")) != null' 'group restored before disable'
samba_tool user disable "$direct_user" >/dev/null
wait_for_user_state "$direct_user" \
  '.isForbidden == true and (.groups | length) == 0' 'disabled and groups cleared'
samba_tool user enable "$direct_user" >/dev/null
wait_for_user_state "$direct_user" \
  ".id == \"$direct_anchor\" and .isForbidden == false and .isDeleted == false and (.groups | index(\"anas/APP_nextcloud\") != null)" \
  're-enabled with original anchor and group'
printf 'disable cleared access and re-enable restored the same identity\n'

section "rename reuses the permanent identity"
samba_tool user rename "$direct_user" --samaccountname="$renamed_user" >/dev/null
wait_for_user_state "$renamed_user" \
  ".id == \"$direct_anchor\" and .name == \"$renamed_user\" and (.groups | index(\"anas/APP_nextcloud\") != null)" \
  'renamed with original anchor'
test "$(casdoor_user "$direct_user" | jq -r '.name // empty')" != "$direct_user"
printf 'rename preserved the anchor and did not retain the old Casdoor name\n'

section "delete forbids, soft-deletes, and clears access"
samba_tool user delete "$renamed_user" >/dev/null
wait_for_user_state "$renamed_user" \
  '.isForbidden == true and .isDeleted == true and (.groups | length) == 0' \
  'deleted and groups cleared'
printf 'delete made the shadow identity unusable and removed its groups\n'

printf '\nCasdoor directory authority E2E tests passed\n'
