#!/bin/bash

if [ -f /opt/meshcentral/meshcentral-data/webserver-cert-private.key  ]; then
  rm /opt/meshcentral/meshcentral-data/webserver-cert-private.key
  rm /opt/meshcentral/meshcentral-data/webserver-cert-public.crt
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
traefik_ip=$( ping $TRAEFIK_HOSTNAME -c 1 | sed '1{s/[^(]*(//;s/).*//;q}')
set_host $TRAEFIK_DOMAIN $traefik_ip

sed -i "s/{{traefik_ip}}/$traefik_ip/g" "/opt/meshcentral/config.json"

node meshcentral/meshcentral --configfile /opt/meshcentral/config.json
