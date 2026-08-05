#!/bin/bash
set -eu

install_internal_ca() {
  local ca="/certs/${ANAS_TLS_INTERNAL_CA_NAME:-anas-internal-ca.crt}"
  if [ -s "$ca" ]; then
    install -m 0644 "$ca" /usr/local/share/ca-certificates/anas-internal-ca.crt
    update-ca-certificates
  fi
}

set_host() {
  local domain="$1"
  local address="$2"
  [ -n "$domain" ] || return 0
  [ -n "$address" ] || return 0
  if grep -qE "[[:space:]]${domain}([[:space:]]|$)" /etc/hosts; then
    sed -i -E "s|^.*[[:space:]]${domain}([[:space:]]|$).*$|${address}\t${domain}|" /etc/hosts
  else
    printf '%s\t%s\n' "$address" "$domain" >> /etc/hosts
  fi
}

resolve_ipv4() {
  local address
  while :; do
    address="$(getent ahostsv4 "$1" | awk 'NR == 1 { print $1 }')"
    if [ -n "$address" ]; then
      printf '%s' "$address"
      return 0
    fi
    echo "Waiting for $1 to resolve" >&2
    sleep 2
  done
}

install_internal_ca

traefik_ip="$(resolve_ipv4 "$TRAEFIK_HOSTNAME")"
collabora_domain="${COLLABORA_DOMAIN_FULL:-}"
collabora_domain="${collabora_domain#*://}"
collabora_domain="${collabora_domain%%/*}"
collabora_domain="${collabora_domain%%:*}"
set_host "$collabora_domain" "$traefik_ip"
set_host "${NEXTCLOUD_DOMAIN:-}" "$traefik_ip"
set_host "${NEXTCLOUD_IAM_HOST:-}" "$traefik_ip"
if [ -n "${SAMBA_DC_HOST:-}" ]; then
  set_host "$SAMBA_DC_HOST" "${SAMBA_DC_HOST_IP:-${HOST_IP:-}}"
fi

rm -f /run/nextcloud-tasks.ready
/usr/local/bin/running.sh &

exec /entrypoint.sh "$@"
