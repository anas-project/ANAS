#!/usr/bin/env bash
# Real ANAS workspace acceptance; the direct Docker fixture is a separate test.
set -Eeuo pipefail
umask 077
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-upgrade-netns.sh"
source "$script_dir/server-require-isolated-docker.sh"
source "$script_dir/server-require-upgrade-proxy-boundary.sh"
exec python3 "$script_dir/immich-workspace-e2e.py" "$@"
