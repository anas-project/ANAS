#!/usr/bin/env bash
set -Eeuo pipefail
# Local execution uses only freshly named networks/volumes/containers and removes
# that exact list. Server execution must additionally source the daemon guard.
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
python3 "$script_dir/valkey-e2e.py"
python3 "$script_dir/integration.py" "$@"
