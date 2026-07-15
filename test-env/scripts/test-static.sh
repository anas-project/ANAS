#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"
log="$REPORT_DIR/static.log"

if go test ./... >"$log" 2>&1; then
  cat "$log"
else
  status=$?
  cat "$log"
  exit "$status"
fi
