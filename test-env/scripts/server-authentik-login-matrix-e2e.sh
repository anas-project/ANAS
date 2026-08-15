#!/usr/bin/env bash
set -euo pipefail

# The provider-specific login probe accepts ANAS_TEST_DOCKER_SOCKET, while the
# shared directory/runtime helpers use the normal Docker client.  Point both at
# the same isolated daemon.
if [ -n "${ANAS_TEST_DOCKER_SOCKET:-}" ]; then
  export DOCKER_HOST="unix://$ANAS_TEST_DOCKER_SOCKET"
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=server-iam-matrix-common.sh
source "$script_dir/server-iam-matrix-common.sh"

authentik="${prefix}authentik"
trap cleanup_matrix_users EXIT HUP INT TERM

run_oidc() {
  local user=$1 outcome=$2 apps=$3 app_admin=${4:-false}
  ANAS_TEST_USERNAME="$user" \
  ANAS_TEST_PASSWORD="$matrix_password" \
  ANAS_TEST_EXPECTED_OUTCOME="$outcome" \
  ANAS_TEST_APPS="$apps" \
  ANAS_TEST_EXPECT_APP_ADMIN="$app_admin" \
  ANAS_TEST_EXPECT_MESHCENTRAL_SITEADMIN="$app_admin" \
  ANAS_TEST_CONTAINER_PREFIX="$prefix" \
    "$script_dir/server-authentik-oidc-login-e2e.sh"
}

wait_authentik_users() {
  local deadline users_csv
  users_csv=$(IFS=,; printf '%s' "${matrix_users[*]}")
  "$docker_cmd" exec "$authentik" ak shell -c \
    "from authentik.tasks.schedules.models import Schedule; [s.send() for s in Schedule.objects.filter(actor_name='authentik.sources.ldap.tasks.ldap_sync') if getattr(s.rel_obj, 'slug', None) == 'samba-ad']" >/dev/null
  deadline=$(( $(date +%s) + matrix_timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if "$docker_cmd" exec "$authentik" ak shell -c \
      "from authentik.core.models import User; from authentik.sources.ldap.models import LDAPSource; from authentik.core.models import UserSourceConnection; s=LDAPSource.objects.get(slug='samba-ad'); names='$users_csv'.split(','); assert all(UserSourceConnection.objects.filter(source=s,user__username=n).exists() for n in names)" \
      >/dev/null 2>&1; then
      return 0
    fi
    sleep 3
  done
  printf 'Authentik did not synchronize all Samba AD matrix users\n' >&2
  return 1
}

create_matrix_users
report_admin_app_membership
ANAS_TEST_IAM_PROVIDER=authentik ANAS_TEST_CONTAINER_PREFIX="$prefix" \
  "$script_dir/server-iam-runtime-contract-e2e.sh"
wait_authentik_users

printf '\n== Authentik: direct APP_nextcloud grant ==\n'
run_oidc "$direct_user" allowed nextcloud
run_oidc "$direct_user" policy-denied meshcentral

printf '\n== Authentik: APP_all grant ==\n'
run_oidc "$all_user" allowed nextcloud,meshcentral

printf '\n== Authentik: recursive role-to-application grant ==\n'
run_oidc "$nested_user" allowed nextcloud
run_oidc "$nested_user" policy-denied meshcentral

printf '\n== Authentik: Admins grant and application admin mappings ==\n'
run_oidc "$admin_user" allowed nextcloud,meshcentral true

printf '\n== Authentik: enabled user without an application group ==\n'
run_oidc "$denied_user" policy-denied nextcloud,meshcentral

printf '\n== Authentik: disabled user remains denied before policy evaluation ==\n'
run_oidc "$disabled_user" auth-denied nextcloud

printf 'PASS: Authentik Samba AD login matrix completed\n'
