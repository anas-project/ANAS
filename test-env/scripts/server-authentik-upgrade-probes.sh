#!/usr/bin/env bash
set -euo pipefail

socket=${ANAS_TEST_DOCKER_SOCKET:-/run/anas-app-docker.sock}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_auth_}
entry_ip=${ANAS_TEST_ENTRY_IP:-10.251.0.2}
entry_port=${ANAS_TEST_ENTRY_PORT:-9000}
domain=${ANAS_TEST_DOMAIN:-nas.test}

docker_cmd() {
  docker -H "unix://$socket" "$@"
}

fail() {
  printf 'authentik upgrade probe failed: %s\n' "$1" >&2
  exit 1
}

container() {
  printf '%s%s' "$prefix" "$1"
}

https_code() {
  local host=$1
  local path=${2:-/}
  curl -skS --noproxy '*' --connect-timeout 10 --max-time 60 --output /dev/null --write-out '%{http_code}' \
    --resolve "$host:$entry_port:$entry_ip" "https://$host:$entry_port$path"
}

https_get() {
  local host=$1
  local path=${2:-/}
  curl -skS --noproxy '*' --connect-timeout 10 --max-time 60 \
    --resolve "$host:$entry_port:$entry_ip" "https://$host:$entry_port$path"
}

expected=(
  "$(container traefik)"
  "$(container samba_dc)"
  "$(container postgres)"
  "$(container authentik)"
  "$(container authentik_worker)"
  "$(container netbird_dashboard)"
  "$(container netbird_signal)"
  "$(container netbird_relay)"
  "$(container netbird_management)"
  "$(container eturnal)"
)

for attempt in $(seq 1 120); do
  ready=true
  for name in "${expected[@]}"; do
    if ! docker_cmd inspect "$name" >/dev/null 2>&1; then
      ready=false
      continue
    fi
    state=$(docker_cmd inspect --format '{{.State.Status}}' "$name")
    health=$(docker_cmd inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$name")
    if [ "$state" != running ] || [ "$health" = starting ] || [ "$health" = unhealthy ]; then
      ready=false
    fi
  done
  if [ "$ready" = true ]; then
    break
  fi
  if [ "$attempt" -eq 120 ]; then
    docker_cmd ps -a
    exit 1
  fi
  sleep 10
done

for name in "${expected[@]}"; do
  state=$(docker_cmd inspect --format '{{.State.Status}}' "$name")
  health=$(docker_cmd inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$name")
  restarts=$(docker_cmd inspect --format '{{.RestartCount}}' "$name")
  printf '%s state=%s health=%s restarts=%s\n' "$name" "$state" "$health" "$restarts"
  test "$state" = running
  test "$health" != unhealthy
  test "$restarts" -eq 0
done

docker_cmd inspect "$(container authentik_init)" --format '{{.State.Status}} {{.State.ExitCode}}' | grep -Fx 'exited 0' >/dev/null
docker_cmd exec "$(container authentik)" ak healthcheck
docker_cmd exec "$(container authentik_worker)" ak healthcheck
docker_cmd exec "$(container authentik)" ak shell -c \
  "from authentik.core.models import Application; from authentik.providers.oauth2.models import OAuth2Provider; app = Application.objects.get(slug='netbird'); assert OAuth2Provider.objects.filter(pk=app.provider_id).exists(); print('authentik_netbird_application=ok provider=oauth2')"
docker_cmd exec "$(container authentik)" test -s /blueprints/anas/anas-clients.yaml ||
  fail "server cannot read the staged anas-clients blueprint"

if ! auth_code=$(https_code "auth.$domain" /); then
  fail "authentik HTTPS endpoint is unreachable"
fi
case "$auth_code" in
  200|302) printf 'authentik_root_status=%s\n' "$auth_code" ;;
  *) fail "authentik HTTPS endpoint returned status $auth_code" ;;
esac
discovery_path=/application/o/netbird/.well-known/openid-configuration
if ! discovery_code=$(https_code "auth.$domain" "$discovery_path"); then
  fail "NetBird OIDC discovery endpoint is unreachable"
fi
test "$discovery_code" = 200 ||
  fail "NetBird OIDC discovery endpoint returned status $discovery_code"
if ! discovery=$(https_get "auth.$domain" "$discovery_path"); then
  fail "NetBird OIDC discovery document could not be read"
fi
printf '%s\n' "$discovery" | jq -e \
  --arg issuer "https://auth.$domain:$entry_port/application/o/netbird/" \
  '.issuer == $issuer and .authorization_endpoint != "" and .token_endpoint != "" and .jwks_uri != ""' >/dev/null ||
  fail "NetBird OIDC discovery metadata is incomplete or has the wrong issuer"

if ! netbird_code=$(https_code "netbird.$domain" /); then
  fail "NetBird HTTPS endpoint is unreachable"
fi
test "$netbird_code" = 200 || fail "NetBird HTTPS endpoint returned status $netbird_code"
if ! key_bytes=$(docker_cmd exec "$(container netbird_management)" sh -lc \
  'jq -r .DataStoreEncryptionKey /etc/netbird/management.json | base64 -d | wc -c'); then
  fail "NetBird management data-store encryption key cannot be decoded"
fi
test "$key_bytes" -eq 32 || fail "NetBird management data-store encryption key is not 32 bytes"
docker_cmd exec "$(container netbird_management)" jq -e \
  --arg discovery "https://auth.$domain:$entry_port/application/o/netbird/.well-known/openid-configuration" \
  '.StoreConfig.Engine == "sqlite" and .HttpConfig.OIDCConfigEndpoint == $discovery' \
  /etc/netbird/management.json >/dev/null ||
  fail "NetBird management config does not use sqlite and the expected OIDC discovery endpoint"

printf '\nauthentik upgrade probes passed\n'
