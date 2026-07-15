#!/bin/bash
set -e

set_host() { # $1 domain, $2 ip
  echo "Set $2 $1"
  if grep -q $1 "/etc/hosts"; then
    hosts=$( sed "s/.*$1.*/$2\t$1/" "/etc/hosts" )
    echo "$hosts" > "/etc/hosts"
  else
    echo -e "$2\t$1" >> "/etc/hosts"
  fi
}

waiting_url() { # $1 url
  url=$1
  http_status=0
  while [ "$http_status" -ne "200" ]; do
    response=$(curl -s -o /dev/null -w "%{http_code}" $url)
    http_status=$response

    if [ "$http_status" -eq "200" ]; then
      echo "URL is accessible: $url"
    else
      echo "URL: $url, is not accessible yet (Status code: $http_status). Retrying..."
      sleep 3 
    fi
  done
}

echo "Set hosts"
traefik_ip=$( ping $TRAEFIK_HOSTNAME -c 1 | sed '1{s/[^(]*(//;s/).*//;q}')
set_host $NETBIRD_DOMAIN $traefik_ip

waiting_url $NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT

curl "${NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT}" -q -o openid-configuration.json

export AUTH_AUTHORITY=$(jq -r '.issuer' openid-configuration.json)

exec /usr/bin/supervisord -c /etc/supervisord.conf