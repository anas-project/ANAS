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
proxy_hits=$(
  git ls-files --cached --others --exclude-standard |
    while IFS= read -r file; do
      [ "${file#test-env/}" = "$file" ] || continue
      [ -f "$file" ] || continue
      grep -IHnE \
        'mirrors\.aliyun\.com|registry\.npmmirror\.com|goproxy\.cn' \
        "$file" || true
    done
)
if [ -n "$proxy_hits" ]; then
  printf '%s\n' "$proxy_hits" >&2
  echo "test-only proxy addresses are forbidden outside test-env/" >&2
  exit 1
fi

status=0
go test ./... >"$log" 2>&1 || status=$?
# Nested modules are excluded from ./... by design: a cask component that is
# built inside its own image keeps its own module so the image build context
# stays the bundle rather than the whole repository. They still have to be
# tested, so each is listed here.
if [ "$status" -eq 0 ]; then
  for module in casks/mods/ddns_go/ddns-go/reconcile; do
    (cd "$module" && go test ./...) >>"$log" 2>&1 || status=$?
  done
fi
if [ "$status" -eq 0 ] && command -v python3 >/dev/null 2>&1; then
  PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s casks/mods/samba_dc/anchor_worker -p 'test_*.py' >>"$log" 2>&1 || status=$?
fi
if [ "$status" -eq 0 ]; then
  sh ./test-env/scripts/test-container-config.sh >>"$log" 2>&1 || status=$?
fi
cat "$log"
exit "$status"
