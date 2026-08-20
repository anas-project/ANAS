#!/bin/bash

set -euo pipefail

ca="/certs/${ANAS_TLS_INTERNAL_CA_NAME:-anas-internal-ca.crt}"
if [ -s "$ca" ]; then
  install -m 0644 "$ca" /usr/local/share/ca-certificates/anas-internal-ca.crt
  update-ca-certificates
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

waiting_url() { # $1 url
  url=$1
  http_status=0
  while [ "$http_status" -ne "200" ]; do
    response=$(curl -sS -o /dev/null -w "%{http_code}" "$url" || true)
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
traefik_ip=
for _ in $(seq 1 30); do
  traefik_ip=$(getent ahostsv4 "$TRAEFIK_HOSTNAME" | awk 'NR == 1 { print $1 }' || true)
  if [ -n "$traefik_ip" ]; then
    break
  fi
  sleep 2
done
if [ -z "$traefik_ip" ]; then
  echo "cannot resolve Traefik host: $TRAEFIK_HOSTNAME" >&2
  exit 1
fi
set_host $NETBIRD_DOMAIN $traefik_ip
auth_domain=$(printf '%s' "$NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT" | sed -E 's#^[a-z]+://([^/:]+).*#\1#')
set_host "$auth_domain" "$traefik_ip"

waiting_url $NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT

curl -fsS "${NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT}" -o openid-configuration.json

export NETBIRD_AUTH_AUTHORITY=$(jq -r '.issuer' openid-configuration.json)
export NETBIRD_AUTH_JWT_CERTS=$(jq -r '.jwks_uri' openid-configuration.json)
export NETBIRD_AUTH_TOKEN_ENDPOINT=$(jq -r '.token_endpoint' openid-configuration.json)
export NETBIRD_AUTH_DEVICE_AUTH_ENDPOINT=$(jq -r '.device_authorization_endpoint' openid-configuration.json) #not support in llng
export NETBIRD_AUTH_PKCE_AUTHORIZATION_ENDPOINT=$(jq -r '.authorization_endpoint' openid-configuration.json)

# NetBird must only peel X-Forwarded-For entries supplied by the actual reverse
# proxy path. Keep the value as JSON so envsubst cannot accidentally turn it
# into an invalid or overly broad configuration.
trusted_http_proxies="${traefik_ip}/32"
if [ -n "${TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS:-}" ]; then
  trusted_http_proxies="${trusted_http_proxies},${TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS}"
fi
export NETBIRD_TRUSTED_HTTP_PROXIES
NETBIRD_TRUSTED_HTTP_PROXIES=$(printf '%s' "$trusted_http_proxies" | jq -Rsc '
  split(",")
  | map(gsub("^[[:space:]]+|[[:space:]]+$"; ""))
  | map(select(length > 0))
')

# Read the encryption key
if test -f 'management.json'; then
    encKey=$(jq -r  ".DataStoreEncryptionKey" management.json)
    if [[ "$encKey" != "null" ]]; then
        export NETBIRD_DATASTORE_ENC_KEY=$encKey
    fi
fi

mkdir -p /etc/netbird

management_variables='$AUTH_AUDIENCE $AUTH_CLIENT_ID $AUTH_CLIENT_SECRET $AUTH_SUPPORTED_SCOPES $NETBIRD_AUTH_AUTHORITY $NETBIRD_AUTH_DEVICE_AUTH_USE_ID_TOKEN $NETBIRD_AUTH_JWT_CERTS $NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT $NETBIRD_AUTH_PKCE_AUTHORIZATION_ENDPOINT $NETBIRD_AUTH_TOKEN_ENDPOINT $NETBIRD_AUTH_USER_ID_CLAIM $NETBIRD_DATASTORE_ENC_KEY $NETBIRD_DOMAIN_PORT $NETBIRD_MGMT_API_PORT $NETBIRD_RELAY_AUTH_SECRET $NETBIRD_RELAY_ENDPOINT $NETBIRD_TRUSTED_HTTP_PROXIES $TURN_DOMAIN_PORT $TURN_SECRET'
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

exec /go/bin/netbird-mgmt management --port 33073 --log-file console --disable-anonymous-metrics=true
