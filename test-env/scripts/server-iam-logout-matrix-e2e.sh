#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/../.." && pwd)
# shellcheck source=server-require-isolated-docker.sh
source "$script_dir/server-require-isolated-docker.sh"

: "${ANAS_TEST_IAM_PROVIDER:?ANAS_TEST_IAM_PROVIDER is required}"
: "${ANAS_TEST_IAM_PROTOCOL:?ANAS_TEST_IAM_PROTOCOL is required}"
: "${ANAS_TEST_USERNAME:?ANAS_TEST_USERNAME is required}"
: "${ANAS_TEST_PASSWORD:?ANAS_TEST_PASSWORD is required}"

provider=$ANAS_TEST_IAM_PROVIDER
protocol=$ANAS_TEST_IAM_PROTOCOL
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_anchor_}
report_dir=${ANAS_TEST_REPORT_DIR:-$repo_root/test-env/reports}
mkdir -p "$report_dir"
umask 077

case "$provider" in
  authentik) provider_version=2026.5.6 ;;
  llng) provider_version=2.23.2 ;;
  casdoor) provider_version=3.143.0 ;;
  *) printf 'unsupported IAM provider: %s\n' "$provider" >&2; exit 2 ;;
esac

if [ -n "${ANAS_TEST_APPS:-}" ]; then
  apps=$ANAS_TEST_APPS
elif [ "$protocol" = oidc ]; then
  apps=nextcloud,meshcentral,netbird,oauth2_proxy
else
  apps=nextcloud
fi

run_case() {
  local app=$1 origin=$2 app_version=$3 pause_container=${4:-}
  local report="$report_dir/iam-logout-${provider}-${protocol}-${app}-${origin}.json"
  printf '== IAM logout: provider=%s/%s protocol=%s app=%s origin=%s ==\n' \
    "$provider" "$provider_version" "$protocol" "$app/$app_version" "$origin"
  ANAS_TEST_APP="$app" \
  ANAS_TEST_LOGOUT_ORIGIN="$origin" \
  ANAS_TEST_PROVIDER_VERSION="$provider_version" \
  ANAS_TEST_APP_VERSION="$app_version" \
  ANAS_TEST_PAUSE_CONTAINER="$pause_container" \
  ANAS_TEST_REPORT_FILE="$report" \
    "$script_dir/server-iam-logout-browser-e2e.sh"
}

IFS=',' read -r -a app_list <<<"$apps"
for app in "${app_list[@]}"; do
  case "$app" in
    nextcloud)
      app_version='34.0.2 / user_oidc 8.10.1 / user_saml 8.2.0'
      run_case "$app" module "$app_version"
      if [ "$provider" != casdoor ]; then
        run_case "$app" iam "$app_version"
      fi
      ;;
    meshcentral)
      [ "$protocol" = oidc ] || { printf 'MeshCentral has no SAML logout case\n' >&2; exit 2; }
      run_case "$app" module '1.2.4'
      ;;
    netbird)
      [ "$protocol" = oidc ] || { printf 'NetBird has no SAML logout case\n' >&2; exit 2; }
      run_case "$app" module 'Dashboard 2.90.9'
      ;;
    oauth2_proxy)
      [ "$protocol" = oidc ] || { printf 'oauth2-proxy has no SAML logout case\n' >&2; exit 2; }
      pause=${ANAS_TEST_IAM_CONTAINER:-${prefix}${provider}}
      run_case "$app" module '7.15.3' "$pause"
      ;;
    *) printf 'unsupported IAM logout application: %s\n' "$app" >&2; exit 2 ;;
  esac
done

printf 'PASS: IAM logout browser matrix completed; reports=%s\n' "$report_dir"
