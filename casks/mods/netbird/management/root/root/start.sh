#!/bin/bash

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

export NETBIRD_AUTH_AUTHORITY=$(jq -r '.issuer' openid-configuration.json)
export NETBIRD_AUTH_JWT_CERTS=$(jq -r '.jwks_uri' openid-configuration.json)
export NETBIRD_AUTH_TOKEN_ENDPOINT=$(jq -r '.token_endpoint' openid-configuration.json)
export NETBIRD_AUTH_DEVICE_AUTH_ENDPOINT=$(jq -r '.device_authorization_endpoint' openid-configuration.json) #not support in llng
export NETBIRD_AUTH_PKCE_AUTHORIZATION_ENDPOINT=$(jq -r '.authorization_endpoint' openid-configuration.json)

# Read the encryption key
if test -f 'management.json'; then
    encKey=$(jq -r  ".DataStoreEncryptionKey" management.json)
    if [[ "$encKey" != "null" ]]; then
        export NETBIRD_DATASTORE_ENC_KEY=$encKey
    fi
fi

mkdir -p /etc/netbird

envsubst </root/management.json.tmpl >/etc/netbird/management.json

exec /go/bin/netbird-mgmt management --port 8000 --log-file console --disable-anonymous-metrics=true
