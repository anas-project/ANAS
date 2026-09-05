#!/usr/bin/env bash
# Source after an upgrade phase has activated a deployment. It derives probe
# addressing from that immutable artifact instead of hard-coding one server.

: "${ANAS_UPGRADE_WORKSPACE:?ANAS_UPGRADE_WORKSPACE is required}"
anas_upgrade_active=$(sed -n 's/^active_deployment: //p' "$ANAS_UPGRADE_WORKSPACE/.anas/state/active.yml")
[[ "$anas_upgrade_active" =~ ^[A-Za-z0-9._-]+$ ]] || {
  echo "active upgrade deployment is missing or invalid" >&2
  exit 1
}
anas_upgrade_env=$ANAS_UPGRADE_WORKSPACE/.anas/deployments/$anas_upgrade_active/modules/traefik/.env
[[ -f "$anas_upgrade_env" && ! -L "$anas_upgrade_env" ]] || {
  echo "active Traefik environment is missing" >&2
  exit 1
}

anas_upgrade_env_value() {
  local key=$1 value
  value=$(sed -n "s/^${key}=//p" "$anas_upgrade_env")
  [[ -n "$value" && "$value" != *$'\n'* ]] || {
    echo "$key is missing or repeated in active Traefik environment" >&2
    exit 1
  }
  printf '%s' "$value"
}

case "${DOCKER_HOST:-}" in
  unix:///*) export ANAS_TEST_DOCKER_SOCKET=${DOCKER_HOST#unix://} ;;
  *) echo "upgrade probes require an explicit unix DOCKER_HOST" >&2; exit 1 ;;
esac
export ANAS_TEST_CONTAINER_PREFIX
ANAS_TEST_CONTAINER_PREFIX=$(anas_upgrade_env_value CONTAINER_PREFIX)
export ANAS_TEST_ENTRY_IP
ANAS_TEST_ENTRY_IP=$(anas_upgrade_env_value HOST_IP)

unset anas_upgrade_active anas_upgrade_env
unset -f anas_upgrade_env_value
