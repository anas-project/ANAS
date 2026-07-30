#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"
log="$REPORT_DIR/build.log"

make_workspace "$RUNTIME_DIR/full" "$CONFIG_DIR/full.yml"

if run_anas build -w "$RUNTIME_DIR/full" >"$log" 2>&1; then
  cat "$log"
else
  status=$?
  cat "$log"
  exit "$status"
fi
