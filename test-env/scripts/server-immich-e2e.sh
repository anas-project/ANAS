#!/usr/bin/env bash
set -Eeuo pipefail
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"
repo_root=$(cd -- "$script_dir/../.." && pwd)
exec bash "$repo_root/modules/immich/tests/container-e2e.sh" "$@"
