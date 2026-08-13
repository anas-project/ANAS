#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

fixture=${1:-previous-patch}
lock="$TEST_ENV_DIR/upgrades/supported/$fixture.lock.yml"
base=${ANAS_UPGRADE_WORKSPACE:-$RUNTIME_DIR/upgrade-$fixture}
marker="anas-upgrade-marker"
log="$REPORT_DIR/upgrade-$fixture.log"
config="$base/config.yml"
config_fixture=${ANAS_UPGRADE_CONFIG:-$CONFIG_DIR/full.yml}

if [ ! -f "$lock" ]; then
  echo "unknown upgrade fixture: $fixture" >&2
  exit 1
fi

cd "$ROOT_DIR"
# Data lives at <workspace>/data; there is no configurable path to rewrite.
# The runtime fixture is injectable so a server-side run can describe an
# isolated Docker daemon's explicit interface and gateway without weakening
# the portable full.yml render fixture.
make_workspace "$base" "$config_fixture"
data_dir="$base/data"

set +e
(
  set -e
  echo "== baseline start: $fixture =="
  run_anas apply --build -w "$base" --update-lock

  echo "== seed migration probes: $fixture =="
  # Write at stable bind-mount roots, not engine-internal data directories.
  # PostgreSQL 18 moved PGDATA below /var/lib/postgresql/18/docker and
  # Nextcloud's persisted application root is /var/www/html.
  docker exec anas_postgres sh -c "printf '%s\n' '$fixture' > /var/lib/postgresql/$marker"
  docker exec anas_mariadb sh -c "printf '%s\n' '$fixture' > /var/lib/mysql/$marker"
  docker exec anas_samba_dc sh -c "printf '%s\n' '$fixture' > /var/lib/samba/$marker"
  docker exec anas_nextcloud sh -c "printf '%s\n' '$fixture' > /var/www/html/$marker"

  echo "== baseline stop: $fixture =="
  run_anas stop -w "$base"

  echo "== seed old module lock: $fixture =="
  cp "$lock" "$base/config.lock.yml"

  echo "== upgrade start: $fixture =="
  run_anas apply --build -w "$base" --update-lock --allow-risky

  echo "== upgrade probes: $fixture =="
  "$TEST_ENV_DIR/scripts/test-upgrade-probes.sh" "$base" "$data_dir"

  echo "== upgrade stop: $fixture =="
  run_anas stop -w "$base"
) >"$log" 2>&1
status=$?
set -e
cat "$log"
if [ "$status" -ne 0 ]; then
  run_anas stop -w "$base" >/dev/null 2>&1 || true
  exit "$status"
fi
