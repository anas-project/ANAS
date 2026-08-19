#!/bin/sh
set -eu

install_internal_ca() {
  ca="$1"
  destination="$2"
  if [ ! -s "$ca" ]; then
    return 0
  fi
  install -m 0644 "$ca" "$destination"
  update-ca-certificates
}

start_nextcloud_cron() {
  ca="$1"
  destination="$2"
  cron_command="$3"

  install_internal_ca "$ca" "$destination"
  exec "$cron_command"
}

if [ "${ANAS_NEXTCLOUD_CRON_LIB_ONLY:-false}" = true ]; then
  return 0 2>/dev/null || exit 0
fi

start_nextcloud_cron \
  "/certs/${ANAS_TLS_INTERNAL_CA_NAME:-anas-internal-ca.crt}" \
  /usr/local/share/ca-certificates/anas-internal-ca.crt \
  /cron.sh
