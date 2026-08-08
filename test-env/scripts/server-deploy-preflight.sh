#!/usr/bin/env bash
# Catch the deployment-level collisions that only surface on a cold create.
#
# Both failures this guards against are invisible on a normal redeploy and then
# abort the deploy the first time the resource is really created:
#
#   * A cask that pins a subnet keeps working for as long as a network of that
#     name already exists, because Compose reuses it whatever subnet it has.
#     Delete that network once and the declared value is used for real -- and
#     if the host already routes it, the deploy fails with "Pool overlaps with
#     other one on this address space".
#
#   * A published host port that a previous, failed create never released is
#     reported as "port is already allocated" even though nothing is listening.
#
# Run it against a rendered deployment before applying.
set -euo pipefail

deployment=${1:-}
if [ -z "$deployment" ]; then
  echo "usage: $0 <deployment-dir>" >&2
  exit 2
fi
test -d "$deployment/casks"

section() { printf '\n== %s ==\n' "$1"; }
status=0

# 172.23.0.0/16 -> 172.23, so two /16s can be compared without a CIDR library.
prefix_of() { printf '%s' "${1%.*.*/*}"; }

env_value() { grep -m1 "^$2=" "$1" 2>/dev/null | cut -d= -f2- || true; }

section "pinned subnets do not collide"
host_routes=$(ip route 2>/dev/null | grep -oE '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+' | sort -u || true)

for env_file in "$deployment"/casks/*/.env; do
  cask=$(basename "$(dirname "$env_file")")
  compose="$(dirname "$env_file")/docker-compose.yml"
  grep -q 'subnet:' "$compose" 2>/dev/null || continue
  network_prefix=$(env_value "$env_file" NETWORK_PREFIX)
  # The compose file references the value; the rendered .env holds it.
  while IFS='=' read -r key value; do
    case "$key" in *SUBNET) ;; *) continue ;; esac
    [ -n "$value" ] || continue
    want=$(prefix_of "$value")
    ours="${network_prefix}${cask}"
    ours_subnets=$(docker network inspect "$ours" \
      --format '{{range .IPAM.Config}}{{.Subnet}} {{end}}' 2>/dev/null || true)
    # Only an existing network already *on the declared subnet* means there is
    # nothing to create. A network of the same name on some other subnet is
    # exactly the case that hides the collision until it is deleted: Compose
    # reuses it and never applies the declared value.
    already_ours=""
    for mine in $ours_subnets; do
      [ "$(prefix_of "$mine")" = "$want" ] && already_ours=yes
    done
    owner=""
    if [ -z "$already_ours" ]; then
      while read -r name; do
        [ -n "$name" ] || continue
        subnets=$(docker network inspect "$name" --format '{{range .IPAM.Config}}{{.Subnet}} {{end}}' 2>/dev/null || true)
        for other in $subnets; do
          [ "$(prefix_of "$other")" = "$want" ] || continue
          owner="another docker network: $name ($other)"
        done
      done <<<"$(docker network ls --format '{{.Name}}' 2>/dev/null)"
      # The host routing table is the only place a network belonging to a
      # different Docker daemon on this machine shows up.
      for route in $host_routes; do
        [ "$(prefix_of "$route")" = "$want" ] || continue
        owner="${owner:-a route already on this host ($route)}"
      done
    fi
    if [ -n "$owner" ]; then
      printf 'COLLISION %s %s=%s is held by %s\n' "$cask" "$key" "$value" "$owner"
      printf '          override it in the deployment config\n'
      status=1
    else
      printf 'ok        %s %s=%s\n' "$cask" "$key" "$value"
    fi
  done <"$env_file"
done

section "published ports are free"
# Ports this deployment already publishes are its own; the check is for a port
# some other tenant of the host holds.
owned_ports=$(docker ps --format '{{.Ports}}' 2>/dev/null |
  grep -oE '0\.0\.0\.0:[0-9]+' | cut -d: -f2 | sort -u || true)

for compose in "$deployment"/casks/*/docker-compose.yml; do
  cask=$(basename "$(dirname "$compose")")
  env_file="$(dirname "$compose")/.env"
  ports=$(grep -oE '^\s+- "\$\{[A-Z_]*PORT[A-Z_]*\}:' "$compose" 2>/dev/null |
    grep -oE '[A-Z_]*PORT[A-Z_]*' | sort -u || true)
  for name in $ports; do
    value=$(env_value "$env_file" "$name")
    [ -n "$value" ] || continue
    if printf '%s\n' $owned_ports | grep -qx "$value"; then
      printf 'ok        %s %s=%s (already published by this deployment)\n' "$cask" "$name" "$value"
    elif ss -tln 2>/dev/null | grep -qE "[:.]$value\b"; then
      printf 'IN USE    %s %s=%s is held by something else on this host\n' "$cask" "$name" "$value"
      status=1
    else
      printf 'ok        %s %s=%s\n' "$cask" "$name" "$value"
    fi
  done
done

if [ "$status" -ne 0 ]; then
  printf '\npreflight found collisions that will abort a cold deploy\n' >&2
  exit 1
fi
printf '\ndeploy preflight passed\n'
