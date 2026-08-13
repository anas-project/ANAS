#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"
. "$ROOT_DIR/modules/nextcloud/nextcloud/root/usr/local/bin/nextcloud-download.sh"

fail() {
  echo "$*" >&2
  exit 1
}

NEXTCLOUD_APPSTORE_URL=https://mirror.example/apps.nextcloud.com/api/v1/
GITHUB_DOWNLOAD_PROXY_PREFIX=https://mirror.example/
export NEXTCLOUD_APPSTORE_URL GITHUB_DOWNLOAD_PROXY_PREFIX

got=$(nextcloud_appstore_platform_url 34.0.2)
[ "$got" = "https://mirror.example/apps.nextcloud.com/api/v1/platform/34.0.2/apps.json" ] || \
  fail "unexpected app store URL: $got"

source_url=https://github.com/example/app/releases/download/v1/app.tar.gz
got=$(nextcloud_download_url "$source_url")
[ "$got" = "https://mirror.example/github.com/example/app/releases/download/v1/app.tar.gz" ] || \
  fail "GitHub URL did not use the configured proxy: $got"

non_github=https://download.example/app.tar.gz
got=$(nextcloud_download_url "$non_github")
[ "$got" = "$non_github" ] || fail "non-GitHub URL was unexpectedly rewritten: $got"

unset GITHUB_DOWNLOAD_PROXY_PREFIX
got=$(nextcloud_download_url "$source_url")
[ "$got" = "$source_url" ] || fail "GitHub URL was rewritten without a proxy: $got"

echo "Nextcloud download proxy URL tests passed"
