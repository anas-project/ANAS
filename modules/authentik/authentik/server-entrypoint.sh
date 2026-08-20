#!/bin/sh
set -eu

password_file=/run/secrets/authentik-break-glass-password
if [ -z "$password_file" ] || [ ! -r "$password_file" ]; then
  echo "missing managed authentik break-glass password file" >&2
  exit 1
fi
AUTHENTIK_BOOTSTRAP_PASSWORD=$(sed -n '1p' "$password_file")
export AUTHENTIK_BOOTSTRAP_PASSWORD

traefik_ip=
for _ in $(seq 1 30); do
  traefik_ip=$(getent ahostsv4 "$TRAEFIK_HOSTNAME" | awk 'NR == 1 { print $1 }')
  if [ -n "$traefik_ip" ]; then
    break
  fi
  sleep 2
done
if [ -z "$traefik_ip" ]; then
  echo "cannot resolve Traefik host: $TRAEFIK_HOSTNAME" >&2
  exit 1
fi

# Override authentik's broad RFC1918 defaults. Only the actual Traefik peer and
# loopback may supply forwarded headers; declared upstream proxies are included
# so a validated multi-proxy X-Forwarded-For chain is parsed consistently.
AUTHENTIK_LISTEN__TRUSTED_PROXY_CIDRS="127.0.0.0/8,::1/128,${traefik_ip}/32"
if [ -n "${TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS:-}" ]; then
  AUTHENTIK_LISTEN__TRUSTED_PROXY_CIDRS="${AUTHENTIK_LISTEN__TRUSTED_PROXY_CIDRS},${TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS}"
fi
export AUTHENTIK_LISTEN__TRUSTED_PROXY_CIDRS

if [ "$(id -u)" = 0 ]; then
  exec setpriv --reuid=1000 --regid=1000 --init-groups ak "$@"
fi
exec ak "$@"
