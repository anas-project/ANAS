#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
. "$script_dir/server-require-isolated-docker.sh"
: "${ANAS_TEST_POSTGRES_IMAGE:?fixed new PostgreSQL Module image required}"
: "${ANAS_TEST_POSTGRES_OLD_IMAGE:?fixed real pgvector 0.8.1 test fixture image required}"

# Record only immutable image IDs and platform, never container environments or
# connection URLs/passwords. The fixture uses its own unique network/volume.
docker image inspect "$ANAS_TEST_POSTGRES_IMAGE" "$ANAS_TEST_POSTGRES_OLD_IMAGE" \
    --format 'fixture_image={{.Id}} platform={{.Os}}/{{.Architecture}}'
printf '%s\n' 'combination=PostgreSQL18.4/Alpine vector0.8.2 cube1.5 earthdistance1.2 preload=empty source_vector=0.8.1'
exec bash "$repo_root/modules/postgres/tests/container-e2e.sh" --isolated-server \
    "$ANAS_TEST_POSTGRES_IMAGE" "$ANAS_TEST_POSTGRES_OLD_IMAGE"
