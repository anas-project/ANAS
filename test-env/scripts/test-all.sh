#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

"$SCRIPT_DIR/test-static.sh"
"$SCRIPT_DIR/test-contract.sh"
"$SCRIPT_DIR/test-lifecycle.sh"
"$SCRIPT_DIR/test-render.sh"
"$SCRIPT_DIR/test-upgrade-render.sh"
"$SCRIPT_DIR/test-compose-config.sh"
"$SCRIPT_DIR/test-build.sh"
"$SCRIPT_DIR/test-smoke.sh"
"$SCRIPT_DIR/test-rollback.sh"
"$SCRIPT_DIR/test-snapshot.sh"
"$SCRIPT_DIR/test-backup.sh"
"$SCRIPT_DIR/test-upgrade.sh"
