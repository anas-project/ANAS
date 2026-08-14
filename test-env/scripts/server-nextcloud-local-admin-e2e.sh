#!/usr/bin/env bash
set -euo pipefail

: "${ANAS_TEST_WORKSPACE:?ANAS_TEST_WORKSPACE is required}"

root=${ANAS_TEST_REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}
socket=${ANAS_TEST_DOCKER_SOCKET:-/run/anas-anchor-docker.sock}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_anchor_}
export DOCKER_HOST="unix://$socket"

credential() {
  (cd "$root" && go run ./cmd/anas admin local credential nextcloud break_glass -w "$ANAS_TEST_WORKSPACE" --json)
}

password_from() {
  CREDENTIAL_JSON=$1 python3 -c 'import json,os; print(json.loads(os.environ["CREDENTIAL_JSON"])["account"]["password"])'
}

username_from() {
  CREDENTIAL_JSON=$1 python3 -c 'import json,os; print(json.loads(os.environ["CREDENTIAL_JSON"])["account"]["username"])'
}

verify_password() {
  local username=$1 password=$2
  printf '%s\n' "$password" | docker exec -i --user www-data "${prefix}nextcloud" php -r \
    'require_once "/var/www/html/lib/base.php"; $p=rtrim(stream_get_contents(STDIN), "\r\n"); $users=\OC::$server->get(\OCP\IUserManager::class); exit($users->checkPassword($argv[1], $p) ? 0 : 1);' \
    "$username"
}

before=$(credential)
username=$(username_from "$before")
old_password=$(password_from "$before")
verify_password "$username" "$old_password"

active=$(awk '/^active_deployment:/ {print $2}' "$ANAS_TEST_WORKSPACE/.anas/state/active.yml")
env_file="$ANAS_TEST_WORKSPACE/.anas/deployments/$active/modules/nextcloud/.env"
if grep -Eq '^NEXTCLOUD_(ADMIN_PASSWORD|LOCAL_ADMIN__BREAK_GLASS_PASSWORD)=' "$env_file"; then
  echo "Nextcloud managed password leaked into deployment env" >&2
  exit 1
fi

(cd "$root" && go run ./cmd/anas admin local rotate nextcloud break_glass -w "$ANAS_TEST_WORKSPACE")
after=$(credential)
new_password=$(password_from "$after")
test "$new_password" != "$old_password"
if verify_password "$username" "$old_password"; then
  echo "old Nextcloud break_glass password still authenticates" >&2
  exit 1
fi
verify_password "$username" "$new_password"

url=$(CREDENTIAL_JSON=$after python3 -c 'import json,os; print(json.loads(os.environ["CREDENTIAL_JSON"])["account"]["url"])')
case "$url" in */login\?direct=1) ;; *) echo "unexpected break_glass URL: $url" >&2; exit 1 ;; esac
echo "PASS: Nextcloud managed break_glass apply and rotate path"
