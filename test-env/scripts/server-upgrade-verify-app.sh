#!/usr/bin/env bash
set -euo pipefail
source "$(dirname -- "$0")/server-upgrade-export-runtime.sh"
"$(dirname -- "$0")/server-upgrade-verify-markers.sh"
"$(dirname -- "$0")/server-app-upgrade-probes.sh"
