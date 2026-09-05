#!/usr/bin/env bash
# Apply and retire narrowly scoped environment compatibility needed to boot an
# exact historical release. This never rewrites the old Module artifact.
set -euo pipefail
umask 077

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <old-modules> <workspace> <prepare-old-start-retry|prepare-running-old|prepare-rollback|cleanup>" >&2
  exit 2
fi

. "$(dirname -- "$0")/server-require-upgrade-netns.sh"
. "$(dirname -- "$0")/server-require-isolated-docker.sh"

old_modules=$(realpath "$1")
workspace=$2
action=$3
state_dir=$workspace/.anas/upgrade-old-compat
state_file=$state_dir/samba-fs-r5-host-macvlan

fail() { printf 'old-release compatibility failed: %s\n' "$1" >&2; exit 1; }
[[ -d "$old_modules" && ! -L "$old_modules" ]] || fail "old Module root is invalid"
[[ "$workspace" = /* ]] || fail "workspace must be absolute"
case "$workspace" in
  /tmp/anas-upgrade-*|/srv/anas-upgrade-*|/data/anas-upgrade-*) ;;
  *) fail "workspace is not in an anas-upgrade test scope" ;;
esac
case "$action" in prepare-old-start-retry|prepare-running-old|prepare-rollback|cleanup) ;; *) fail "unknown action: $action" ;; esac

valid_ipv4() {
  local value=$1 a b c d octet
  [[ "$value" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || return 1
  IFS=. read -r a b c d <<< "$value"
  for octet in "$a" "$b" "$c" "$d"; do
    ((10#$octet <= 255)) || return 1
  done
}

state_value() {
  local key=$1 value
  value=$(sed -n "s/^${key}=//p" "$state_file")
  [[ -n "$value" && "$value" != *$'\n'* ]] || fail "$key is missing or repeated in compatibility state"
  printf '%s' "$value"
}

cleanup_compat() {
  [[ -f "$state_file" && ! -L "$state_file" ]] || {
    printf '%s\n' 'upgrade_old_compat=not-required action=cleanup'
    return
  }
  local bridge transport previous
  bridge=$(state_value bridge)
  transport=$(state_value transport)
  previous=$(state_value previous_proxy_arp)
  [[ "$bridge" =~ ^[A-Za-z0-9_.-]{1,15}$ ]] || fail "invalid bridge in compatibility state"
  valid_ipv4 "$transport" || fail "invalid transport IP in compatibility state"
  [[ "$previous" == 0 || "$previous" == 1 ]] || fail "invalid proxy_arp value in compatibility state"
  ip neigh del proxy "$transport" dev "$bridge" >/dev/null 2>&1 || true
  sysctl -q -w "net.ipv4.conf.$bridge.proxy_arp=$previous"
  rm -f -- "$state_file"
  rmdir -- "$state_dir" 2>/dev/null || true
  printf 'upgrade_old_compat=retired action=cleanup bridge=%s\n' "$bridge"
}

if [[ "$action" == cleanup ]]; then
  cleanup_compat
  exit 0
fi

# Authentik r8's cold-database health window is shorter than its full schema
# migration on the supported low-resource server. The failed exact-old start
# has already persisted completed migrations, so a bounded retry can resume the
# same old image and data without changing or rebuilding the release artifact.
# The runner invokes this only for a structured `start_failed` result.
if [[ "$action" == prepare-old-start-retry ]]; then
  manifest=$old_modules/authentik/module.yml
  [[ -f "$manifest" && ! -L "$manifest" ]] || exit 3
  version=$(awk '$1 == "version:" { print $2; exit }' "$manifest")
  revision=$(awk '$1 == "revision:" { print $2; exit }' "$manifest")
  [[ "$version" == 2026.5.6 && "$revision" == 8 ]] || exit 3
  case "${ANAS_UPGRADE_SUITE:-}" in
    modules-authentik|modules-ddns) ;;
    *) exit 3 ;;
  esac
  printf 'upgrade_old_compat=active action=prepare-old-start-retry release=authentik-2026.5.6-r8 old_artifact_unchanged=true reason=cold-database-health-window\n'
  exit 0
fi

manifest=$old_modules/samba_fs/module.yml
if [[ ! -f "$manifest" || -L "$manifest" ]]; then
  printf 'upgrade_old_compat=not-required action=%s reason=no-old-samba-fs\n' "$action"
  exit 0
fi
version=$(awk '$1 == "version:" { print $2; exit }' "$manifest")
revision=$(awk '$1 == "revision:" { print $2; exit }' "$manifest")
if [[ "$version" != 4.23.6 || "$revision" != 5 ]]; then
  printf 'upgrade_old_compat=not-required action=%s reason=unmatched-release\n' "$action"
  exit 0
fi

active=$(sed -n 's/^active_deployment: //p' "$workspace/.anas/state/active.yml")
[[ "$active" =~ ^[A-Za-z0-9._-]+$ ]] || fail "active deployment is missing or invalid"
env_file=$workspace/.anas/deployments/$active/modules/samba_fs/.env
if [[ ! -e "$env_file" ]]; then
  printf 'upgrade_old_compat=not-required action=%s reason=samba-fs-not-selected\n' "$action"
  exit 0
fi
[[ -f "$env_file" && ! -L "$env_file" ]] || fail "active Samba FS environment is missing"

env_value() {
  local key=$1 value
  value=$(sed -n "s/^${key}=//p" "$env_file")
  [[ -n "$value" && "$value" != *$'\n'* ]] || fail "$key is missing or repeated in active Samba FS environment"
  printf '%s' "$value"
}

prefix=$(env_value CONTAINER_PREFIX)
bridge=$(env_value VLAN_BRIDGE_INTERFACE)
transport=$(env_value SAMBA_DC_DNS_SERVER)
target=$(env_value HOST_LAN_IP)
zone=$(env_value SAMBA_DC_DOMAIN)
dc_fqdn=$(env_value SAMBA_DC_DC_DOMAIN)
fs_hostname=$(env_value SAMBA_FS_HOSTNAME)
record_name=$(printf '%s' "$fs_hostname" | tr '[:upper:]' '[:lower:]')

[[ "$prefix" =~ ^[A-Za-z0-9_.-]+_$ ]] || fail "invalid container prefix"
[[ "$bridge" =~ ^[A-Za-z0-9_.-]{1,15}$ ]] || fail "invalid bridge interface"
[[ "$record_name" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || fail "invalid Samba FS record name"
[[ "$zone" =~ ^[A-Za-z0-9.-]+$ && "$dc_fqdn" =~ ^[A-Za-z0-9.-]+$ ]] || fail "invalid AD DNS identity"
valid_ipv4 "$transport" || fail "invalid historical DC transport IP"
valid_ipv4 "$target" || fail "invalid Samba FS IP"
ip link show dev "$bridge" >/dev/null 2>&1 || fail "macvlan bridge is absent: $bridge"

dc_container=${prefix}samba_dc
fs_container=${prefix}samba_fs
for container in "$dc_container" "$fs_container"; do
  docker inspect "$container" >/dev/null 2>&1 || fail "expected test container is absent: $container"
done

if [[ ! -f "$state_file" ]]; then
  previous=$(sysctl -n "net.ipv4.conf.$bridge.proxy_arp")
  [[ "$previous" == 0 || "$previous" == 1 ]] || fail "unexpected proxy_arp value"
  install -d -m 0700 "$state_dir"
  {
    printf 'bridge=%s\n' "$bridge"
    printf 'transport=%s\n' "$transport"
    printf 'previous_proxy_arp=%s\n' "$previous"
  } >"$state_file"
  chmod 0600 "$state_file"
fi
sysctl -q -w "net.ipv4.conf.$bridge.proxy_arp=1"
ip neigh replace proxy "$transport" dev "$bridge"

query_addresses() {
  docker exec "$dc_container" samba-tool dns query \
    127.0.0.1 "$zone" "$record_name" A -P 2>/dev/null |
    awk '/^[[:space:]]*A: / { print $2 }'
}

addresses=$(query_addresses || true)
desired_present=false
while IFS= read -r address; do
  [[ -n "$address" ]] || continue
  if [[ "$address" == "$target" ]]; then
    desired_present=true
    continue
  fi
  valid_ipv4 "$address" || fail "unexpected AD DNS address"
  docker exec "$dc_container" samba-tool dns delete \
    127.0.0.1 "$zone" "$record_name" A "$address" -P >/dev/null
done <<< "$addresses"
if [[ "$desired_present" != true ]]; then
  docker exec "$dc_container" samba-tool dns add \
    127.0.0.1 "$zone" "$record_name" A "$target" -P >/dev/null
fi
[[ "$(query_addresses)" == "$target" ]] || fail "historical Samba FS A record did not converge"

if [[ "$action" == prepare-running-old ]]; then
  docker restart "$fs_container" >/dev/null
fi
printf 'upgrade_old_compat=active action=%s release=samba_fs-4.23.6-r5 old_artifact_unchanged=true dc_identity=%s\n' \
  "$action" "$dc_fqdn"
