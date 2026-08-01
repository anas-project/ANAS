#!/bin/bash

set -euo pipefail

if [ -f /opt/meshcentral/meshcentral-data/webserver-cert-private.key  ]; then
  rm -f /opt/meshcentral/meshcentral-data/webserver-cert-private.key \
    /opt/meshcentral/meshcentral-data/webserver-cert-public.crt
fi

if [ -f /opt/meshcentral/certs/$LEGO_KEY_NAME  ]; then
  ln -s /opt/meshcentral/certs/$LEGO_KEY_NAME /opt/meshcentral/meshcentral-data/webserver-cert-private.key 
  ln -s /opt/meshcentral/certs/$LEGO_CERT_NAME /opt/meshcentral/meshcentral-data/webserver-cert-public.crt 
fi


set_host() { # $1 domain, $2 ip
  echo "Set $2 $1"
  if grep -q $1 "/etc/hosts"; then
    hosts=$( sed "s/.*$1.*/$2\t$1/" "/etc/hosts" )
    echo "$hosts" > "/etc/hosts"
  else
    echo -e "$2\t$1" >> "/etc/hosts"
  fi
}

echo "Set traefik hosts"
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
set_host "$TRAEFIK_DOMAIN" "$traefik_ip"

mkdir -p /run/anas
TRAEFIK_IP="$traefik_ip" node /opt/anas/configure.js \
  /opt/anas/config.base.json /run/anas/config.json

exec node meshcentral/meshcentral --configfile /run/anas/config.json
