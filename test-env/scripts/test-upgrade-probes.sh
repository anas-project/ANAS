#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

base=${1:-"$RUNTIME_DIR/upgrade-previous-patch"}
data_dir=${2:-"$base/data"}
marker="anas-upgrade-marker"
log="$REPORT_DIR/upgrade-probes-$(basename "$base").log"

cd "$ROOT_DIR"

check_file() {
  path=$1
  if [ ! -f "$path" ]; then
    echo "missing migrated data marker: $path" >&2
    return 1
  fi
}

check_container_running() {
  container=$1
  if ! docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null | grep -qx true; then
    echo "container is not running: $container" >&2
    return 1
  fi
}

{
  echo "== container state =="
  for container in \
    anas_traefik \
    anas_postgres \
    anas_mariadb \
    anas_samba_dc \
    anas_nextcloud
  do
    check_container_running "$container"
  done

  echo "== host data markers =="
  check_file "$data_dir/postgres/$marker"
  check_file "$data_dir/mariadb/$marker"
  check_file "$data_dir/samba_dc/var/$marker"
  check_file "$data_dir/nextcloud/nextcloud/$marker"

  echo "== service probes =="
  docker exec anas_postgres pg_isready -U postgres
  docker exec anas_mariadb sh -c 'test -d /config'
  docker exec anas_samba_dc sh -c 'test -d /var/lib/samba'
  docker exec anas_nextcloud sh -c 'test -d /data'

  echo "== cask lock updated =="
  for cask in core lego samba_dc samba_fs traefik keycloak llng mariadb postgres eturnal nextcloud collabora meshcentral netbird lam ddns freeradius; do
    current=$(awk '
      $1 == "version:" { print $2; exit }
    ' "$ROOT_DIR/casks/mods/$cask/cask.yml")
    if ! awk -v cask="$cask" -v version="$current" '
      $1 == cask ":" { in_cask = 1; next }
      in_cask && $1 == "version:" { found = ($2 == version); exit }
      in_cask && /^[^[:space:]]/ { exit }
      END { exit found ? 0 : 1 }
    ' "$base/cask.lock.yml"; then
      echo "lock version for $cask was not updated to $current" >&2
      exit 1
    fi
  done
} >"$log" 2>&1

cat "$log"
