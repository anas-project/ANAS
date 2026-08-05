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

container() {
  printf '%s%s' "$prefix" "$1"
}

https_code() {
  local host=$1
  local path=${2:-/}
  curl -skS --connect-timeout 10 --max-time 60 --output /dev/null --write-out '%{http_code}' \
    --resolve "$host:$entry_port:$entry_ip" "https://$host:$entry_port$path"
}

https_get() {
  local host=$1
  local path=${2:-/}
  curl -skS --connect-timeout 10 --max-time 60 \
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
  "from authentik.core.models import Application; assert Application.objects.filter(slug='netbird').exists(); print('authentik_netbird_application=ok')"
docker_cmd exec "$(container authentik)" test -s /blueprints/anas/anas-clients.yaml

case "$(https_code "auth.$domain" /)" in 200|302) ;; *) exit 1 ;; esac
discovery=$(https_get "auth.$domain" /application/o/netbird/.well-known/openid-configuration)
printf '%s\n' "$discovery" | jq -e \
  --arg issuer "https://auth.$domain:$entry_port/application/o/netbird/" \
  '.issuer == $issuer and .authorization_endpoint != "" and .token_endpoint != "" and .jwks_uri != ""' >/dev/null

test "$(https_code "netbird.$domain" /)" = 200
key_bytes=$(docker_cmd exec "$(container netbird_management)" sh -lc \
  'jq -r .DataStoreEncryptionKey /etc/netbird/management.json | base64 -d | wc -c')
test "$key_bytes" -eq 32
docker_cmd exec "$(container netbird_management)" jq -e \
  --arg discovery "https://auth.$domain:$entry_port/application/o/netbird/.well-known/openid-configuration" \
  '.StoreConfig.Engine == "sqlite" and .HttpConfig.OIDCConfigEndpoint == $discovery' \
  /etc/netbird/management.json >/dev/null

printf '\nauthentik upgrade probes passed\n'
