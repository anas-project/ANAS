#!/usr/bin/env bash
set -euo pipefail
source "$(dirname -- "$0")/server-upgrade-export-runtime.sh"
"$(dirname -- "$0")/server-upgrade-verify-markers.sh"

container="${ANAS_TEST_CONTAINER_PREFIX:?}casdoor"
docker exec "$container" /opt/anas/bin/casdoor-helper healthcheck
docker exec "$container" sh -ceu '
  test "$(stat -c %u:%g:%a /conf/app.conf)" = 1000:1000:600
  test "$(awk "/^Uid:/ {print \$2}" /proc/1/status)" = 1000
'
issuer=$(docker exec "$container" printenv CASDOOR_DOMAIN_FULL)
docker exec "$container" sh -ceu 'wget -qO- http://127.0.0.1:8000/.well-known/openid-configuration' |
  jq -e --arg issuer "$issuer" '.issuer == $issuer and (.jwks_uri | length > 0)' >/dev/null
printf 'casdoor_upgrade_verify=pass phase=%s\n' "${ANAS_UPGRADE_PHASE:-unknown}"
