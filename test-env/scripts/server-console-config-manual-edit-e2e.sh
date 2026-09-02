#!/usr/bin/env sh
# REQUIREMENTS: CONSOLE-R-044
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

fail() {
  printf 'R-044 E2E failed: %s\n' "$1" >&2
  if [ -n "${daemon_log:-}" ] && [ -f "$daemon_log" ]; then
    printf '%s\n' '--- anasd log ---' >&2
    tail -n 100 "$daemon_log" >&2 || true
  fi
  exit 1
}

if [ "$(id -u)" -ne 0 ]; then
  fail "run as root so anasd can enforce its production service-config policy"
fi

anas_bin=${ANAS_BIN:-}
anasd_bin=${ANASD_BIN:-}
module_root=${ANAS_MODULE_ROOT:-}
python_bin=${PYTHON:-$(command -v python3 || true)}
curl_bin=${CURL:-$(command -v curl || true)}

for item in "$anas_bin" "$anasd_bin"; do
  [ -n "$item" ] && [ -f "$item" ] && [ ! -L "$item" ] && [ -x "$item" ] ||
    fail "ANAS_BIN and ANASD_BIN must name executable regular files"
done
[ -n "$module_root" ] && [ -d "$module_root" ] && [ ! -L "$module_root" ] ||
  fail "ANAS_MODULE_ROOT must name an unpacked Module directory"
[ -n "$python_bin" ] || fail "python3 is required"
[ -n "$curl_bin" ] || fail "curl is required"

workdir=$(mktemp -d /tmp/anas-r044.XXXXXX)
workspace=$workdir/workspace
console_store=$workdir/console-store
service_config=$workdir/anasd.yml
cookie_jar=$workdir/cookies.txt
daemon_log=$workdir/anasd.log
daemon_pid=

cleanup() {
  if [ -n "$daemon_pid" ] && kill -0 "$daemon_pid" 2>/dev/null; then
    kill "$daemon_pid" 2>/dev/null || true
    count=0
    while kill -0 "$daemon_pid" 2>/dev/null && [ "$count" -lt 50 ]; do
      sleep 0.1
      count=$((count + 1))
    done
    if kill -0 "$daemon_pid" 2>/dev/null; then
      kill -KILL "$daemon_pid" 2>/dev/null || true
    fi
    wait "$daemon_pid" 2>/dev/null || true
  fi
  case "$workdir" in
    /tmp/anas-r044.*) rm -rf -- "$workdir" ;;
    *) printf 'refusing to clean unexpected path: %s\n' "$workdir" >&2 ;;
  esac
}
trap cleanup EXIT HUP INT TERM

port=$(
  "$python_bin" - <<'PY'
import socket

sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()
PY
)
origin="http://127.0.0.1:$port"

"$anas_bin" init "$workspace" -y >/dev/null 2>&1 || fail "initialize isolated workspace"
"$python_bin" - "$workspace/.anas/module-view.json" "$module_root" <<'PY'
import json
import os
import sys

target, module_root = sys.argv[1:]
with open(target, "w", encoding="utf-8") as stream:
    json.dump({
        "api_version": "anas.module-view/v1",
        "digest": "r044-e2e",
        "module_root": os.path.abspath(module_root),
        "installations": {},
    }, stream, indent=2)
    stream.write("\n")
os.chmod(target, 0o600)
PY

{
  printf '%s\n' 'api_version: anas.console-config/v1'
  printf '%s\n' 'mode: loopback'
  printf 'port: %s\n' "$port"
  printf 'console_store: %s\n' "$console_store"
  printf '%s\n' 'workspaces:'
  printf '%s\n' '  - id: main'
  printf '    path: %s\n' "$workspace"
} >"$service_config"
chmod 0600 "$service_config"

token_document=$("$anas_bin" console token --config "$service_config" --ttl 15m --json) ||
  fail "issue bootstrap token"
bootstrap_token=$(printf '%s' "$token_document" | "$python_bin" -c 'import json,sys; print(json.load(sys.stdin)["token"])')
[ -n "$bootstrap_token" ] || fail "bootstrap token output was empty"

"$anasd_bin" --config "$service_config" >"$daemon_log" 2>&1 &
daemon_pid=$!

csrf_body=$workdir/csrf.json
attempt=0
while :; do
  status=$("$curl_bin" -sS -c "$cookie_jar" -o "$csrf_body" -w '%{http_code}' \
    "$origin/api/v1/auth/csrf" 2>/dev/null || true)
  [ "$status" = 200 ] && break
  kill -0 "$daemon_pid" 2>/dev/null || fail "anasd exited before accepting HTTP requests"
  attempt=$((attempt + 1))
  [ "$attempt" -lt 100 ] || fail "anasd did not become ready"
  sleep 0.1
done
preauth_csrf=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["csrf_token"])' "$csrf_body")

exchange_body=$workdir/exchange.json
exchange_status=$("$curl_bin" -sS -b "$cookie_jar" -c "$cookie_jar" -o "$exchange_body" -w '%{http_code}' \
  -H "Origin: $origin" -H "X-CSRF-Token: $preauth_csrf" -H 'Content-Type: application/json' \
  --data-binary "{\"token\":\"$bootstrap_token\"}" "$origin/api/v1/auth/bootstrap/exchange")
[ "$exchange_status" = 200 ] || fail "bootstrap exchange returned HTTP $exchange_status"
session_csrf=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["csrf_token"])' "$exchange_body")
bootstrap_token=
token_document=

get_headers=$workdir/config-get.headers
get_body=$workdir/config-get.json
get_status=$("$curl_bin" -sS -b "$cookie_jar" -D "$get_headers" -o "$get_body" -w '%{http_code}' \
  "$origin/api/v1/workspaces/main/config")
[ "$get_status" = 200 ] || fail "initial config GET returned HTTP $get_status"
etag=$(sed -n 's/^[Ee][Tt][Aa][Gg]:[[:space:]]*//p' "$get_headers" | tr -d '\r' | tail -n 1)
ETAG=$etag "$python_bin" -c 'import os,re,sys; sys.exit(0 if re.fullmatch(r"\"cfgv-[0-9a-f]{64}\"", os.environ["ETAG"]) else 1)' ||
  fail "initial config GET did not return a strong opaque ETag"

candidate=$workdir/config-put.json
"$python_bin" - "$get_body" "$candidate" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    snapshot = json.load(stream)
with open(sys.argv[2], "w", encoding="utf-8") as stream:
    json.dump({"config": snapshot["config"], "sensitive": {}}, stream,
              separators=(",", ":"))
PY

state_path=$workspace/.anas/config-managed.yml
state_digest_before=$("$python_bin" -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$state_path")
printf '\n# external R-044 edit\n' >>"$workspace/config.yml"
manual_digest=$("$python_bin" -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$workspace/config.yml")

failed_get_body=$workdir/config-get-after-edit.json
failed_get_status=$("$curl_bin" -sS -b "$cookie_jar" -o "$failed_get_body" -w '%{http_code}' \
  "$origin/api/v1/workspaces/main/config")
[ "$failed_get_status" = 412 ] || fail "config GET after external edit returned HTTP $failed_get_status"

failed_put_body=$workdir/config-put-after-edit.json
failed_put_status=$("$curl_bin" -sS -b "$cookie_jar" -o "$failed_put_body" -w '%{http_code}' \
  -X PUT -H "Origin: $origin" -H "X-CSRF-Token: $session_csrf" \
  -H "If-Match: $etag" -H 'Content-Type: application/json' \
  --data-binary "@$candidate" "$origin/api/v1/workspaces/main/config")
[ "$failed_put_status" = 412 ] || fail "config PUT after external edit returned HTTP $failed_put_status"

"$python_bin" - "$failed_get_body" "$failed_put_body" <<'PY'
import json
import sys

for path in sys.argv[1:]:
    with open(path, encoding="utf-8") as stream:
        problem = json.load(stream)
    assert problem["status"] == 412, problem
    assert problem["code"] == "config_precondition_failed", problem
PY

manual_digest_after=$("$python_bin" -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$workspace/config.yml")
state_digest_after=$("$python_bin" -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$state_path")
[ "$manual_digest_after" = "$manual_digest" ] || fail "failed PUT overwrote the externally edited config.yml"
[ "$state_digest_after" = "$state_digest_before" ] || fail "failed PUT changed managed config state"
kill -0 "$daemon_pid" 2>/dev/null || fail "anasd exited during the request sequence"

printf 'environment=%s %s filesystem=%s\n' \
  "$(. /etc/os-release; printf '%s-%s' "$ID" "$VERSION_ID")" \
  "$(uname -m)" "$(stat -f -c %T "$workdir")"
printf 'R-044 get_after_manual_edit=%s put_after_manual_edit=%s problem_code=config_precondition_failed external_bytes_preserved=yes managed_state_preserved=yes\n' \
  "$failed_get_status" "$failed_put_status"
printf '%s\n' 'PASS: CONSOLE-R-044 external config.yml edit fails closed with HTTP 412'
