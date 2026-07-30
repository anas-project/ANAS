#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"
log="$REPORT_DIR/server-isolated-build.log"
config=${ANAS_SERVER_CONFIG:-$TEST_ENV_DIR/server-full-runtime.yml}

make_workspace "$RUNTIME_DIR/server-full" "$config"

if run_anas build -w "$RUNTIME_DIR/server-full" >"$log" 2>&1; then
  cat "$log"
else
  status=$?
  cat "$log"
  exit "$status"
fi
