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

management_variables='$AUTH_AUDIENCE $AUTH_CLIENT_ID $AUTH_CLIENT_SECRET $AUTH_SUPPORTED_SCOPES $NETBIRD_AUTH_AUTHORITY $NETBIRD_AUTH_DEVICE_AUTH_USE_ID_TOKEN $NETBIRD_AUTH_JWT_CERTS $NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT $NETBIRD_AUTH_PKCE_AUTHORIZATION_ENDPOINT $NETBIRD_AUTH_TOKEN_ENDPOINT $NETBIRD_AUTH_USER_ID_CLAIM $NETBIRD_DOMAIN_PORT $NETBIRD_MGMT_API_PORT $TURN_DOMAIN_PORT'
for name in $(printf '%s\n' "$management_variables" | grep -o '[A-Z][A-Z0-9_]*'); do
    eval 'present=${'"$name"'+x}'
    if [[ "$present" != x ]]; then
        echo "missing required environment variable: $name" >&2
        exit 1
    fi
done
envsubst "$management_variables" </root/management.json.envsubst >/etc/netbird/management.json.tmp
if grep -n '\$[A-Z][A-Z0-9_]*' /etc/netbird/management.json.tmp; then
    echo "unresolved variables in management.json" >&2
    exit 1
fi
jq empty /etc/netbird/management.json.tmp
mv /etc/netbird/management.json.tmp /etc/netbird/management.json

exec /go/bin/netbird-mgmt management --port 8000 --log-file console --disable-anonymous-metrics=true
