#!/usr/bin/env bash

# Shared fixture functions for the two provider-specific matrix entry points.
# The entry points remain separate deployments and separate tests; only the AD
# account setup is shared so they cannot drift into different user semantics.

server_script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]:-$0}")" && pwd)
# shellcheck source=server-require-isolated-docker.sh
source "$server_script_dir/server-require-isolated-docker.sh"

docker_cmd=${DOCKER_CMD:-docker}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_anchor_}
dc="${prefix}samba_dc"
# A burst may be picked up by the audit path immediately or by the configured
# five-minute reconciliation fallback. The E2E timeout must cover both paths.
matrix_timeout=${ANAS_TEST_MATRIX_TIMEOUT:-420}
matrix_suffix=${ANAS_TEST_MATRIX_SUFFIX:-$(date +%H%M%S)}
matrix_password=${ANAS_TEST_MATRIX_PASSWORD:-Anas-Iam-${matrix_suffix}-E2e!}

direct_user="iad${matrix_suffix}"
all_user="iaa${matrix_suffix}"
admin_user="iam${matrix_suffix}"
denied_user="ian${matrix_suffix}"
disabled_user="iax${matrix_suffix}"
nested_user="iar${matrix_suffix}"
nested_group="ROLE_iam_${matrix_suffix}"
matrix_users=("$direct_user" "$all_user" "$admin_user" "$denied_user" "$disabled_user" "$nested_user")

dc_exec() {
  "$docker_cmd" exec "$dc" "$@"
}

cleanup_matrix_users() {
  local user
  for user in "${matrix_users[@]}"; do
    dc_exec samba-tool user delete "$user" >/dev/null 2>&1 || true
  done
  dc_exec samba-tool group delete "$nested_group" >/dev/null 2>&1 || true
}

wait_anchor() {
  local user=$1 deadline anchor
  deadline=$(( $(date +%s) + matrix_timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    anchor=$(dc_exec samba-tool user show "$user" --attributes=anasIdentityAnchor 2>/dev/null \
      | sed -n 's/^anasIdentityAnchor: //p')
    if [ -n "$anchor" ]; then
      return 0
    fi
    sleep 2
  done
  printf 'identity anchor was not written for %s\n' "$user" >&2
  return 1
}

create_matrix_users() {
  local user
  cleanup_matrix_users
  for user in "${matrix_users[@]}"; do
    dc_exec samba-tool user add "$user" "$matrix_password" --userou='OU=People' >/dev/null
    dc_exec samba-tool user setexpiry "$user" --noexpiry >/dev/null
    dc_exec samba-tool user rename "$user" --display-name="IAM E2E $user" >/dev/null
  done
  dc_exec samba-tool group addmembers APP_nextcloud "$direct_user" >/dev/null
  dc_exec samba-tool group addmembers APP_all "$all_user" >/dev/null
  dc_exec samba-tool group addmembers Admins "$admin_user" >/dev/null
  dc_exec samba-tool group addmembers APP_nextcloud "$disabled_user" >/dev/null
  dc_exec samba-tool group add "$nested_group" --groupou='OU=Role,OU=Groups' >/dev/null
  dc_exec samba-tool group addmembers "$nested_group" "$nested_user" >/dev/null
  dc_exec samba-tool group addmembers APP_nextcloud "$nested_group" >/dev/null
  dc_exec samba-tool user disable "$disabled_user" >/dev/null
  for user in "${matrix_users[@]}"; do
    wait_anchor "$user"
  done
}

report_admin_app_membership() {
  local admin app direct all
  admin=$(dc_exec printenv SAMBA_DC_ADMIN_NAME)
  all=$(dc_exec samba-tool group listmembers APP_all | grep -Fx "$admin" || true)
  printf 'bootstrap_admin user=%s APP_all=%s' "$admin" "$([ -n "$all" ] && printf member || printf absent)"
  test -z "$all"
  for app in nextcloud meshcentral netbird; do
    direct=$(dc_exec samba-tool group listmembers "APP_$app" | grep -Fx "$admin" || true)
    printf ' APP_%s=%s' "$app" "$([ -n "$direct" ] && printf member || printf absent)"
    test -z "$direct"
  done
  printf '\n'
}
