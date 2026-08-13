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
  docker exec anas_mariadb sh -c 'test -d /var/lib/mysql'
  docker exec anas_samba_dc sh -c 'test -d /var/lib/samba'
  docker exec anas_nextcloud sh -c 'test -d /var/www/html'

  echo "== module lock updated =="
  selected_modules=$(awk '
    $0 == "modules:" { inside_modules = 1; next }
    inside_modules && /^[^[:space:]]/ { exit }
    inside_modules && /^[[:space:]]+[[:alnum:]_]+:$/ {
      match($0, /[^[:space:]]/)
      indent = RSTART - 1
      if (module_indent == 0) module_indent = indent
      if (indent != module_indent) next
      module = $0
      sub(/^[[:space:]]+/, "", module)
      sub(/:$/, "", module)
      print module
    }
  ' "$base/config.lock.yml")
  if [ -z "$selected_modules" ]; then
    echo "config.lock.yml contains no selected modules" >&2
    exit 1
  fi
  for module in $selected_modules; do
    current=$(awk '
      $1 == "version:" { print $2; exit }
    ' "$ROOT_DIR/modules/$module/module.yml")
    if ! awk -v module="$module" -v version="$current" '
      $1 == module ":" { in_module = 1; next }
      in_module && $1 == "version:" { found = ($2 == version); exit }
      in_module && /^[^[:space:]]/ { exit }
      END { exit found ? 0 : 1 }
    ' "$base/config.lock.yml"; then
      echo "lock version for $module was not updated to $current" >&2
      exit 1
    fi
  done
} >"$log" 2>&1

cat "$log"
