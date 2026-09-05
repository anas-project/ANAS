#!/usr/bin/env bash
set -euo pipefail

for name in SAMBA_DC_ADMIN_NAME SAMBA_DC_ADMIN_PASSWORD SAMBA_DC_DC_DOMAIN SAMBA_DC_DNS_SERVER SAMBA_DC_DOMAIN SAMBA_FS_HOSTNAME; do
  eval 'present=${'"$name"'+x}'
  if [[ "$present" != x || -z "${!name}" ]]; then
    echo "missing required environment variable: $name" >&2
    exit 1
  fi
done

target_ip=${1:?usage: register_ad_dns.sh TARGET_IP}
if [[ ! "$target_ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
  echo "invalid Samba FS IPv4 address" >&2
  exit 1
fi
IFS=. read -r octet1 octet2 octet3 octet4 <<< "$target_ip"
for octet in "$octet1" "$octet2" "$octet3" "$octet4"; do
  if ((10#$octet > 255)); then
    echo "invalid Samba FS IPv4 address" >&2
    exit 1
  fi
done

record_name=$(printf '%s' "$SAMBA_FS_HOSTNAME" | tr '[:upper:]' '[:lower:]')
if [[ ! "$record_name" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]]; then
  echo "invalid Samba FS DNS record name" >&2
  exit 1
fi

ccache_path="${XDG_RUNTIME_DIR:-/run}/anas-samba-fs-dns-registration.$$"
export KRB5CCNAME="FILE:$ccache_path"
cleanup() {
  kdestroy >/dev/null 2>&1 || true
  rm -f -- "$ccache_path"
}
trap cleanup EXIT

printf '%s\n' "$SAMBA_DC_ADMIN_PASSWORD" | kinit "$SAMBA_DC_ADMIN_NAME"

query_addresses() {
  samba-tool dns query \
    "$SAMBA_DC_DC_DOMAIN" "$SAMBA_DC_DOMAIN" "$record_name" A \
    --use-kerberos=required 2>/dev/null |
    awk '/^[[:space:]]*A: / { print $2 }'
}

addresses=$(query_addresses || true)
desired_present=false
while IFS= read -r address; do
  [[ -n "$address" ]] || continue
  if [[ "$address" == "$target_ip" ]]; then
    desired_present=true
    continue
  fi
  samba-tool dns delete \
    "$SAMBA_DC_DC_DOMAIN" "$SAMBA_DC_DOMAIN" "$record_name" A "$address" \
    --use-kerberos=required >/dev/null
done <<< "$addresses"

if [[ "$desired_present" != true ]]; then
  samba-tool dns add \
    "$SAMBA_DC_DC_DOMAIN" "$SAMBA_DC_DOMAIN" "$record_name" A "$target_ip" \
    --use-kerberos=required >/dev/null
fi

addresses=$(query_addresses)
if [[ "$addresses" != "$target_ip" ]]; then
  echo "AD DNS A record did not converge to the Samba FS address" >&2
  exit 1
fi

fqdn="$record_name.$SAMBA_DC_DOMAIN"
resolved=$(host "$fqdn" "$SAMBA_DC_DNS_SERVER" 2>/dev/null |
  awk '/ has address / { print $NF }')
if [[ "$resolved" != "$target_ip" ]]; then
  echo "AD DNS resolver did not return the Samba FS address" >&2
  exit 1
fi

echo "Registered $fqdn at $target_ip"
