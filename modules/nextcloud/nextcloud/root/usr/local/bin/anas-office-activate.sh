#!/bin/bash
# TEST_CASES: TEMP-T-021
set -euo pipefail
umask 077

# Nextcloud must become healthy before its dependent Collabora starts. Wait in
# this container's background task, rather than caching a proxy startup error.
ready_file=${1:?Office readiness path is required}
wait_seconds=${2:?Office startup timeout is required}
if [[ "$ready_file" != /* || ! "$wait_seconds" =~ ^[1-9][0-9]{0,4}$ ]]; then
  echo "Invalid Office startup arguments" >&2
  exit 1
fi
rm -f -- "$ready_file"
discovery_file=$(mktemp)
trap 'rm -f -- "$discovery_file"' EXIT
deadline=$((SECONDS + wait_seconds))

while (( SECONDS < deadline )); do
  if curl --fail --silent --show-error --connect-timeout 5 --max-time 15 \
      --output "$discovery_file" "${COLLABORA_DOMAIN_FULL:?Collabora URL is required}/hosting/discovery" &&
      grep -q '<wopi-discovery' "$discovery_file" &&
      timeout 30s runuser -u www-data -- php /var/www/html/occ richdocuments:activate-config; then
    touch "$ready_file"
    echo "Nextcloud Office configuration activated"
    exit 0
  fi
  echo "Waiting for Collabora Office configuration" >&2
  sleep 5
done

echo "Nextcloud Office initialization timed out" >&2
exit 1
