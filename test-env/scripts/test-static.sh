#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"
log="$REPORT_DIR/static.log"

if find casks -type f \( -name '*.erb' -o -name '*.j2' -o -name '*.j3' -o -name '*.tmpl' \) -print -quit | grep -q .; then
  echo "legacy template suffixes are forbidden under casks/" >&2
  exit 1
fi
if grep -R -n -E '<%=|<%[[:space:]]+if|#\{envs\[' casks; then
  echo "legacy ERB syntax is forbidden under casks/" >&2
  exit 1
fi

status=0
go test ./... >"$log" 2>&1 || status=$?
if [ "$status" -eq 0 ]; then
  sh ./test-env/scripts/test-container-config.sh >>"$log" 2>&1 || status=$?
fi
cat "$log"
exit "$status"
