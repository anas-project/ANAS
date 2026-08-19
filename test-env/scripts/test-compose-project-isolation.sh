#!/usr/bin/env sh
# Deterministic E2E for the Docker Compose workspace boundary. It proves that
# custom container prefixes scope project names, cleanup addresses only that
# scope, and a reused project name cannot mutate another workspace.
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

anas_bin="$ROOT_DIR/.anas-test/bin/anas"
fake_bin="$TEST_ENV_DIR/fakes"
base="$RUNTIME_DIR/compose-project-isolation"
prod_ws="$base/production"
test_ws="$base/e2e"
collision_ws="$base/collision"
parallel_a_ws="$base/parallel-a"
parallel_b_ws="$base/parallel-b"
prod_config="$base/production.yml"
test_config="$base/e2e.yml"
parallel_a_config="$base/parallel-a.yml"
parallel_b_config="$base/parallel-b.yml"
log="$REPORT_DIR/compose-project-isolation-docker.log"

mkdir -p "$(dirname -- "$anas_bin")" "$base" "$REPORT_DIR"
go build -o "$anas_bin" ./cmd/anas

write_config() {
  path=$1
  prefix=$2
  domain=$3
  port=$4
  cat >"$path" <<EOF
modules:
  lego: {}
global:
  base_domain: $domain
  email: admin@$domain
  timezone: Asia/Shanghai
  virtual_domain: true
  container_prefix: ${prefix}
  network_prefix: ${prefix}
env:
  TRAEFIK_BASE_PORT: "$port"
EOF
}

rm -rf "$prod_ws" "$test_ws" "$collision_ws" "$parallel_a_ws" "$parallel_b_ws"
write_config "$prod_config" anas_iso_prod_ prod.invalid 19471
write_config "$test_config" anas_iso_e2e_ e2e.invalid 19472
write_config "$parallel_a_config" anas_iso_pa_ pa.invalid 19473
write_config "$parallel_b_config" anas_iso_pb_ pb.invalid 19474

export PATH="$fake_bin:$PATH"
export ANAS_FAKE_DOCKER_LOG="$log"
: >"$log"

apply_fixture() {
  workspace=$1
  config=$2
  "$anas_bin" init "$workspace" -y >/dev/null
  "$anas_bin" config import "$config" -w "$workspace" >/dev/null
  "$anas_bin" apply -w "$workspace" --root "$ROOT_DIR" --update-lock --no-snapshot -y >/dev/null
}

apply_fixture "$prod_ws" "$prod_config"
apply_fixture "$test_ws" "$test_config"

grep -Eq -- '--project-name anas_iso_prod_lego( |$).* up( |$)' "$log"
grep -Eq -- '--project-name anas_iso_e2e_lego( |$).* up( |$)' "$log"

: >"$log"
"$anas_bin" stop -w "$test_ws" >/dev/null
grep -Eq -- '--project-name anas_iso_e2e_lego( |$).* down( |$)' "$log"
if grep -Eq -- '--project-name anas_iso_prod_lego( |$).* (up|down|start|stop|restart|rm|run)( |$)' "$log"; then
  echo "test cleanup crossed into the production Compose project" >&2
  exit 1
fi

: >"$log"
apply_fixture "$parallel_a_ws" "$parallel_a_config" &
parallel_a_pid=$!
apply_fixture "$parallel_b_ws" "$parallel_b_config" &
parallel_b_pid=$!
wait "$parallel_a_pid"
wait "$parallel_b_pid"
grep -Eq -- '--project-name anas_iso_pa_lego( |$).* up( |$)' "$log"
grep -Eq -- '--project-name anas_iso_pb_lego( |$).* up( |$)' "$log"

# Reusing the production prefix from another workspace must fail before Docker
# Compose receives any mutating command. The owner comes from the same standard
# working_dir label a real Compose container exposes.
prod_id=$(sed -n 's/^active_deployment: //p' "$prod_ws/.anas/state/active.yml")
export ANAS_FAKE_DOCKER_COMPOSE_OWNER_PROJECT=anas_iso_prod_lego
export ANAS_FAKE_DOCKER_COMPOSE_OWNER_DIR="$prod_ws/.anas/deployments/$prod_id/modules/lego"
: >"$log"
"$anas_bin" init "$collision_ws" -y >/dev/null
"$anas_bin" config import "$prod_config" -w "$collision_ws" >/dev/null
if "$anas_bin" apply -w "$collision_ws" --root "$ROOT_DIR" --update-lock --no-snapshot -y \
  >"$base/collision.out" 2>&1; then
  echo "cross-workspace Compose project collision was accepted" >&2
  exit 1
fi
grep -Fq 'owned by workspace' "$base/collision.out"
if grep -Eq 'docker compose .* (up|down|start|stop|restart|rm|run)( |$)' "$log"; then
  echo "collision guard ran after a mutating Compose command" >&2
  exit 1
fi

unset ANAS_FAKE_DOCKER_COMPOSE_OWNER_PROJECT ANAS_FAKE_DOCKER_COMPOSE_OWNER_DIR
printf 'Compose project isolation E2E passed\n'
