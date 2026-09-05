#!/usr/bin/env bash
# Source before a Module upgrade suite creates containers. Docker client proxy
# settings are injected into every new container; without private-network
# exclusions, a reverse proxy can send Docker backends to an outbound proxy.

anas_upgrade_proxy_fail() {
  printf '%s\n' \
    'refusing Module upgrade E2E: Docker client proxy noProxy must include localhost, loopback and all RFC1918 CIDRs' >&2
  exit 2
}

anas_upgrade_docker_config=${DOCKER_CONFIG:-${HOME:-}/.docker}
anas_upgrade_docker_client_config=$anas_upgrade_docker_config/config.json
if [[ -f "$anas_upgrade_docker_client_config" ]]; then
  command -v jq >/dev/null 2>&1 || anas_upgrade_proxy_fail
  anas_upgrade_proxy_profile=$(jq -ce --arg host "${DOCKER_HOST:-}" \
    '(.proxies[$host] // .proxies.default // {}) | if type == "object" then . else error("invalid proxy profile") end' \
    "$anas_upgrade_docker_client_config") || anas_upgrade_proxy_fail
  anas_upgrade_proxy_url=$(jq -r '.httpProxy // .httpsProxy // .ftpProxy // ""' \
    <<<"$anas_upgrade_proxy_profile") || anas_upgrade_proxy_fail
  if [[ -n "$anas_upgrade_proxy_url" ]]; then
    anas_upgrade_no_proxy=$(jq -r '.noProxy // "" | if type == "string" then . else error("invalid noProxy") end' \
      <<<"$anas_upgrade_proxy_profile") || anas_upgrade_proxy_fail
    anas_upgrade_no_proxy=",$(tr -d '[:space:]' <<<"$anas_upgrade_no_proxy"),"
    if [[ "$anas_upgrade_no_proxy" != *",*,"* ]]; then
      for anas_upgrade_required_bypass in \
        localhost 127.0.0.1 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16; do
        [[ "$anas_upgrade_no_proxy" == *",$anas_upgrade_required_bypass,"* ]] ||
          anas_upgrade_proxy_fail
      done
    fi
  fi
fi

unset anas_upgrade_docker_config anas_upgrade_docker_client_config
unset anas_upgrade_proxy_profile anas_upgrade_proxy_url anas_upgrade_no_proxy
unset anas_upgrade_required_bypass
unset -f anas_upgrade_proxy_fail
