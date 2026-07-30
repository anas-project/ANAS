#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

fixture=${1:-previous-patch}
lock="$TEST_ENV_DIR/upgrades/supported/$fixture.lock.yml"
base="$RUNTIME_DIR/upgrade-$fixture"
marker="anas-upgrade-marker"
log="$REPORT_DIR/upgrade-$fixture.log"
config="$base/config.yml"

if [ ! -f "$lock" ]; then
  echo "unknown upgrade fixture: $fixture" >&2
  exit 1
fi

cd "$ROOT_DIR"
# Data lives at <workspace>/data; there is no configurable path to rewrite.
make_workspace "$base" "$CONFIG_DIR/full.yml"
data_dir="$base/data"

if {
  echo "== baseline start: $fixture =="
  run_anas apply --build -w "$base" --update-lock

  echo "== seed migration probes: $fixture =="
  docker exec anas_postgres sh -c "printf '%s\n' '$fixture' > /var/lib/postgresql/data/$marker"
  docker exec anas_mariadb sh -c "printf '%s\n' '$fixture' > /config/$marker"
  docker exec anas_samba_dc sh -c "printf '%s\n' '$fixture' > /var/lib/samba/$marker"
  docker exec anas_nextcloud sh -c "printf '%s\n' '$fixture' > /data/$marker"

  echo "== baseline stop: $fixture =="
  run_anas stop -w "$base"

  echo "== seed old cask lock: $fixture =="
  cp "$lock" "$base/config.lock.yml"

  echo "== upgrade start: $fixture =="
  run_anas apply --build -w "$base" --update-lock --allow-risky

  echo "== upgrade probes: $fixture =="
  "$TEST_ENV_DIR/scripts/test-upgrade-probes.sh" "$base" "$data_dir"

  echo "== upgrade stop: $fixture =="
  run_anas stop -w "$base"
} >"$log" 2>&1; then
  cat "$log"
else
  status=$?
  cat "$log"
  run_anas stop -w "$base" >/dev/null 2>&1 || true
  exit "$status"
fi
