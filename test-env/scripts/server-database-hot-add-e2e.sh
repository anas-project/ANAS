#!/usr/bin/env sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
workspace=${1:?usage: server-database-hot-add-e2e.sh WORKSPACE}
docker_host=${DOCKER_HOST:-unix:///run/anas-anchor-docker.sock}
export DOCKER_HOST=$docker_host
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/server-require-isolated-docker.sh"

run_anas() {
  (cd "$root" && go run ./cmd/anas "$@")
}

apply_config() {
  config=$1
	run_anas config import "$config" -w "$workspace"
  run_anas apply -w "$workspace" --root "$root" --update-lock --build \
    --allow-risky --no-snapshot -y
}

container_id() {
  docker -H "$docker_host" inspect --format '{{.Id}}' "$1"
}

postgres_container=anas_anchor_postgres
nextcloud_container=anas_anchor_nextcloud

if [ ! -d "$workspace/.anas" ]; then
  run_anas init "$workspace" -y >/dev/null
fi

echo '== database provider baseline =='
apply_config "$root/test-env/server-database-hot-add-base.yml"
postgres_before=$(container_id "$postgres_container")

echo '== hot-add Nextcloud and its database resource =='
apply_config "$root/test-env/server-database-hot-add-full.yml"
postgres_after=$(container_id "$postgres_container")
if [ "$postgres_before" != "$postgres_after" ]; then
  echo "PostgreSQL was recreated while adding Nextcloud" >&2
  exit 1
fi

active=$(sed -n 's/^active_deployment: //p' "$workspace/.anas/state/active.yml")
nextcloud_env="$workspace/.anas/deployments/$active/modules/nextcloud/.env"
test -f "$nextcloud_env"
grep -qx 'NEXTCLOUD_DB_USERNAME=nextcloud' "$nextcloud_env"
grep -qx 'NEXTCLOUD_DB_NAME=nextcloud' "$nextcloud_env"
test -f "$workspace/.anas/state/resources/nextcloud.primary_database.yml"
grep -qx 'status: ready' "$workspace/.anas/state/resources/nextcloud.primary_database.yml"

owner=$(docker -H "$docker_host" exec "$postgres_container" \
  psql -U postgres -d postgres -Atc \
  "select pg_get_userbyid(datdba) from pg_database where datname='nextcloud'")
test "$owner" = nextcloud
flags=$(docker -H "$docker_host" exec "$postgres_container" \
  psql -U postgres -d postgres -Atc \
  "select rolsuper::int || '|' || rolcreatedb::int || '|' || rolcreaterole::int from pg_roles where rolname='nextcloud'")
test "$flags" = "0|0|0"

if docker -H "$docker_host" ps --format '{{.Names}}' | grep -q 'postgres_provision'; then
  echo "one-shot PostgreSQL provisioner remained running" >&2
  exit 1
fi

nextcloud_before=$(container_id "$nextcloud_container")
echo '== idempotent re-apply =='
apply_config "$root/test-env/server-database-hot-add-full.yml"
test "$(container_id "$postgres_container")" = "$postgres_before"
test "$(container_id "$nextcloud_container")" = "$nextcloud_before"

echo 'database resource hot-add e2e tests passed'
