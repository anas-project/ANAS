#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=server-iam-matrix-common.sh
source "$script_dir/server-iam-matrix-common.sh"
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
    "$script_dir/server-llng-oidc-login-e2e.sh"
}

create_matrix_users
report_admin_app_membership
ANAS_TEST_IAM_PROVIDER=llng ANAS_TEST_CONTAINER_PREFIX="$prefix" \
  "$script_dir/server-iam-runtime-contract-e2e.sh"

printf '\n== LLNG: direct APP_nextcloud grant ==\n'
run_oidc "$direct_user" allowed nextcloud
run_oidc "$direct_user" policy-denied meshcentral

printf '\n== LLNG: APP_all grant ==\n'
run_oidc "$all_user" allowed nextcloud,meshcentral

printf '\n== LLNG: recursive role-to-application grant ==\n'
run_oidc "$nested_user" allowed nextcloud
run_oidc "$nested_user" policy-denied meshcentral

printf '\n== LLNG: Admins grant and application admin mappings ==\n'
run_oidc "$admin_user" allowed nextcloud,meshcentral true

printf '\n== LLNG: enabled user without an application group ==\n'
run_oidc "$denied_user" policy-denied nextcloud,meshcentral

printf '\n== LLNG: disabled user remains denied before policy evaluation ==\n'
run_oidc "$disabled_user" auth-denied nextcloud

printf 'PASS: LLNG Samba AD login matrix completed\n'
