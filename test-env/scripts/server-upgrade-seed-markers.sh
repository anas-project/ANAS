#!/usr/bin/env bash
set -euo pipefail

. "$(dirname -- "$0")/server-require-isolated-docker.sh"
: "${ANAS_UPGRADE_WORKSPACE:?ANAS_UPGRADE_WORKSPACE is required}"
. "$(dirname -- "$0")/server-upgrade-export-runtime.sh"
"$(dirname -- "$0")/server-upgrade-verify-services.sh" >/dev/null
marker_file="$ANAS_UPGRADE_WORKSPACE/.anas/upgrade-e2e-markers"
marker="anas-upgrade-$(date -u +%Y%m%dT%H%M%SZ)-$$"
: >"$marker_file"
chmod 0600 "$marker_file"

while IFS= read -r container; do
  case "$container" in "$ANAS_TEST_CONTAINER_PREFIX"*) ;; *) continue ;; esac
  path=
  case "$container" in
    *postgres) path=/var/lib/postgresql/anas-upgrade-e2e-marker ;;
    *mariadb) path=/var/lib/mysql/anas-upgrade-e2e-marker ;;
    *samba_dc) path=/var/lib/samba/anas-upgrade-e2e-marker ;;
    *samba_fs) path=/var/lib/samba/anas-upgrade-e2e-marker ;;
    *nextcloud) path=/var/www/html/anas-upgrade-e2e-marker ;;
    *llng) path=/var/lib/lemonldap-ng/anas-upgrade-e2e-marker ;;
    *lam) path=/var/lib/ldap-account-manager/anas-upgrade-e2e-marker ;;
    *meshcentral) path=/opt/meshcentral/meshcentral-data/anas-upgrade-e2e-marker ;;
    *netbird_management) path=/var/lib/netbird/anas-upgrade-e2e-marker ;;
    *eturnal) path=/var/lib/eturnal/anas-upgrade-e2e-marker ;;
    *ddns-go|*ddns_go) path=/root/anas-upgrade-e2e-marker ;;
    *lego) path=/certs/anas-upgrade-e2e-marker ;;
  esac
  [[ -n "$path" ]] || continue
  parent=${path%/*}
  if docker exec "$container" test -d "$parent" >/dev/null 2>&1; then
    printf '%s' "$marker" | docker exec -i "$container" sh -ceu 'cat >"$1"' sh "$path"
    printf '%s\t%s\t%s\n' "$container" "$path" "$marker" >>"$marker_file"
  fi
done < <(docker ps --format '{{.Names}}')

[[ -s "$marker_file" ]] || {
  echo "no persistent service marker target was found" >&2
  exit 1
}
printf 'upgrade_seed=pass markers=%s\n' "$(wc -l <"$marker_file" | tr -d ' ')"
