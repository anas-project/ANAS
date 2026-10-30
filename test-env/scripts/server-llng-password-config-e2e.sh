#!/usr/bin/env bash
set -euo pipefail
# TEST_CASES: LLNG-PWD-T-001
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"
llng=${ANAS_TEST_LLNG_CONTAINER:?explicit isolated LLNG container required}
cli=/usr/share/lemonldap-ng/bin/lemonldap-ng-cli
assert_flags() {
  docker exec "$llng" "$cli" -json get ldapChangePasswordAsUser ldapAllowResetExpiredPassword |
    python3 -c 'import json,sys; c=json.load(sys.stdin); assert all(int(c[k]) == 1 for k in ("ldapChangePasswordAsUser","ldapAllowResetExpiredPassword")); print(json.dumps(c))'
}
assert_flags
# Simulate a persisted configuration predating this change. The real Module
# startup must repair both values, without deleting its configuration volume.
docker exec "$llng" "$cli" --user=www-data --group=www-data -yes 1 -force 1 set \
  ldapChangePasswordAsUser 0 ldapAllowResetExpiredPassword 0 >/dev/null
docker exec "$llng" rm -f /run/llng-configured
docker restart "$llng" >/dev/null
ready=false
for ((i=0; i<60; i++)); do
  if docker exec "$llng" test -f /run/llng-configured 2>/dev/null; then
    ready=true
    break
  fi
  sleep 2
done
[ "$ready" = true ]
assert_flags
printf 'PASS: fresh and persisted LLNG password settings\n'
