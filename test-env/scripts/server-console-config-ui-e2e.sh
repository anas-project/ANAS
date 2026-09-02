#!/usr/bin/env sh
# REQUIREMENTS: CONSOLE-R-120 CONSOLE-R-126
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
umask 077

command_name=${1:-}
anas_bin=${ANAS_BIN:-}
anasd_bin=${ANASD_BIN:-}
module_root=${ANAS_MODULE_ROOT:-}
python_bin=${PYTHON:-$(command -v python3 || true)}
curl_bin=${CURL:-$(command -v curl || true)}
daemon_log=

fail() {
  printf 'R-120 config UI E2E failed: %s\n' "$1" >&2
  if [ -n "${daemon_log:-}" ] && [ -f "$daemon_log" ]; then
    printf '%s\n' '--- anasd log ---' >&2
    tail -n 120 "$daemon_log" >&2 || true
  fi
  exit 1
}

require_root() {
  [ "$(id -u)" -eq 0 ] || fail "run as root so anasd can enforce production file policy"
}

require_regular_executable() {
  [ -n "$1" ] && [ -f "$1" ] && [ ! -L "$1" ] && [ -x "$1" ] ||
    fail "$2 must name an executable regular, non-symlink file"
}

validate_run_id() {
  case "$1" in
    ''|*[!A-Za-z0-9._-]*) fail "ANAS_E2E_RUN_ID must use only letters, digits, dot, underscore, or hyphen" ;;
  esac
}

workdir_for_run() {
  validate_run_id "$1"
  printf '/tmp/anas-r120.%s\n' "$1"
}

pid_matches_daemon() {
  pid=$1
  expected=$2
  [ -n "$pid" ] && [ -d "/proc/$pid" ] || return 1
  actual=$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)
  [ "$actual" = "$(readlink -f "$expected")" ]
}

stop_daemon() {
  directory=$1
  [ -f "$directory/anasd.pid" ] && [ -f "$directory/anasd.path" ] || return 0
  pid=$(sed -n '1p' "$directory/anasd.pid")
  expected=$(sed -n '1p' "$directory/anasd.path")
  if pid_matches_daemon "$pid" "$expected"; then
    kill "$pid" 2>/dev/null || true
    count=0
    while pid_matches_daemon "$pid" "$expected" && [ "$count" -lt 100 ]; do
      sleep 0.1
      count=$((count + 1))
    done
    if pid_matches_daemon "$pid" "$expected"; then
      kill -KILL "$pid" 2>/dev/null || true
    fi
  fi
}

cleanup_run() {
  directory=$1
  case "$directory" in
    /tmp/anas-r120.*) ;;
    *) fail "refuse cleanup outside /tmp/anas-r120.*" ;;
  esac
  stop_daemon "$directory"
  rm -rf -- "$directory"
}

runtime_tree_digest() {
  workspace_path=$1
  "$python_bin" - "$workspace_path" <<'PY'
import hashlib
import os
import stat
import sys

workspace = os.path.abspath(sys.argv[1])
digest = hashlib.sha256()
for relative_root in ("deployments", "data", "userdata"):
    root = os.path.join(workspace, relative_root)
    if not os.path.lexists(root):
        digest.update((relative_root + "\0missing\0").encode())
        continue
    for current, directories, files in os.walk(root, topdown=True, followlinks=False):
        directories.sort()
        files.sort()
        relative = os.path.relpath(current, workspace)
        for name in ["."] + directories + files:
            path = current if name == "." else os.path.join(current, name)
            item = relative if name == "." else os.path.relpath(path, workspace)
            metadata = os.lstat(path)
            digest.update(item.encode() + b"\0" + oct(stat.S_IMODE(metadata.st_mode)).encode() + b"\0")
            if stat.S_ISLNK(metadata.st_mode):
                digest.update(b"link\0" + os.readlink(path).encode() + b"\0")
            elif stat.S_ISREG(metadata.st_mode):
                digest.update(b"file\0")
                with open(path, "rb") as stream:
                    for block in iter(lambda: stream.read(1024 * 1024), b""):
                        digest.update(block)
            else:
                digest.update(("kind:" + str(stat.S_IFMT(metadata.st_mode)) + "\0").encode())
print(digest.hexdigest())
PY
}

setup_run() {
  require_root
  run_id=${ANAS_E2E_RUN_ID:-}
  port=${ANAS_E2E_PORT:-}
  validate_run_id "$run_id"
  case "$port" in
    ''|*[!0-9]*) fail "ANAS_E2E_PORT must be a numeric FRP-range port" ;;
  esac
  [ "$port" -ge 7700 ] && [ "$port" -le 7799 ] ||
    fail "ANAS_E2E_PORT must be between 7700 and 7799"
  require_regular_executable "$anas_bin" ANAS_BIN
  require_regular_executable "$anasd_bin" ANASD_BIN
  [ -n "$module_root" ] && [ -d "$module_root" ] && [ ! -L "$module_root" ] ||
    fail "ANAS_MODULE_ROOT must name an unpacked Module directory"
  [ -n "$python_bin" ] || fail "python3 is required"
  [ -n "$curl_bin" ] || fail "curl is required"

  workdir=$(workdir_for_run "$run_id")
  [ ! -e "$workdir" ] || fail "test workdir already exists: $workdir"
  mkdir -m 0700 "$workdir"
  workspace=$workdir/workspace
  console_store=$workdir/console-store
  service_config=$workdir/anasd.yml
  daemon_log=$workdir/anasd.log
  trap 'cleanup_run "$workdir"' EXIT HUP INT TERM

  initial_config=$workdir/initial-config.yml
  {
    printf '%s\n' 'module_source: official'
    printf '%s\n' 'modules:'
    printf '%s\n' '  samba_dc:'
    printf '%s\n' '    config:'
    printf '%s\n' '      domain: r120.test'
    printf '%s\n' 'global:'
    printf '%s\n' '  base_domain: r120.test'
    printf '%s\n' '  email: admin@r120.test'
    printf '%s\n' '  timezone: UTC'
    printf '%s\n' 'env:'
    printf '%s\n' '  CONTAINER_PREFIX: anas_r120_'
    printf '%s\n' '  NETWORK_PREFIX: anas_r120_'
  } >"$initial_config"
  chmod 0600 "$initial_config"
  init_output=$workdir/init.json
  if ! "$anas_bin" init "$workspace" --config "$initial_config" --module-root "$module_root" -y --json \
    >"$init_output" 2>&1; then
    sed -n '1,120p' "$init_output" >&2 || true
    fail "initialize isolated workspace"
  fi
  "$python_bin" - "$workspace/.anas/module-view.json" "$module_root" <<'PY'
import json
import os
import sys

target, module_root = sys.argv[1:]
with open(target, "w", encoding="utf-8") as stream:
    json.dump({
        "api_version": "anas.module-view/v1",
        "digest": "r120-config-ui-e2e",
        "module_root": os.path.abspath(module_root),
        "installations": {},
    }, stream, indent=2)
    stream.write("\n")
os.chmod(target, 0o600)
PY

  {
    printf '%s\n' 'api_version: anas.console-config/v1'
    printf '%s\n' 'mode: lan'
    printf 'port: %s\n' "$port"
    printf 'console_store: %s\n' "$console_store"
    printf '%s\n' 'workspaces:'
    printf '%s\n' '  - id: main'
    printf '    path: %s\n' "$workspace"
  } >"$service_config"
  chmod 0600 "$service_config"

  token_document=$workdir/bootstrap-token.json
  token_error=$workdir/bootstrap-token.log
  if ! "$anas_bin" console token --config "$service_config" --ttl 30m --json \
    >"$token_document" 2>"$token_error"; then
    sed -n '1,120p' "$token_error" >&2 || true
    fail "issue bootstrap token"
  fi
  : >"$token_error"
  chmod 0600 "$token_document"

  runtime_tree_digest "$workspace" >"$workdir/runtime-before.sha256"
  chmod 0600 "$workdir/runtime-before.sha256"
  printf '%s\n' "$anasd_bin" >"$workdir/anasd.path"
  chmod 0600 "$workdir/anasd.path"
  nohup "$anasd_bin" --config "$service_config" </dev/null >"$daemon_log" 2>&1 &
  daemon_pid=$!
  printf '%s\n' "$daemon_pid" >"$workdir/anasd.pid"
  chmod 0600 "$workdir/anasd.pid"

  origin="http://127.0.0.1:$port"
  ready_body=$workdir/system.json
  attempt=0
  while :; do
    status=$("$curl_bin" -sS -o "$ready_body" -w '%{http_code}' "$origin/api/v1/system" 2>/dev/null || true)
    [ "$status" = 200 ] && break
    pid_matches_daemon "$daemon_pid" "$anasd_bin" || fail "anasd exited before accepting HTTP requests"
    attempt=$((attempt + 1))
    [ "$attempt" -lt 100 ] || fail "anasd did not become ready"
    sleep 0.1
  done
  "$python_bin" - "$ready_body" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1], encoding="utf-8"))
assert body["console_state"] == "bootstrap", body
assert body["workspace_ids"] == ["main"], body
PY

  metadata=$workdir/metadata.json
  "$python_bin" - "$metadata" "$workdir" "$origin" "$port" <<'PY'
import json
import os
import sys

path, workdir, origin, port = sys.argv[1:]
with open(path, "w", encoding="utf-8") as stream:
    json.dump({
        "workdir": workdir,
        "origin": origin,
        "port": int(port),
        "bootstrap_token_document": os.path.join(workdir, "bootstrap-token.json"),
        "workspace_id": "main",
        "field_path": "global.timezone",
        "initial_value": "UTC",
        "saved_value": "Asia/Singapore",
        "fieldless_module": "freeradius",
    }, stream, separators=(",", ":"))
    stream.write("\n")
os.chmod(path, 0o600)
PY

  trap - EXIT HUP INT TERM
  printf 'workdir=%s\n' "$workdir"
  printf 'metadata=%s\n' "$metadata"
  printf 'origin=%s\n' "$origin"
  printf 'backend=127.0.0.1:%s\n' "$port"
  printf '%s\n' 'ready=browser_config_ui'
}

verify_run() {
  require_root
  run_id=${ANAS_E2E_RUN_ID:-}
  workdir=$(workdir_for_run "$run_id")
  [ -d "$workdir" ] || fail "test workdir does not exist: $workdir"
  daemon_log=$workdir/anasd.log
  workspace=$workdir/workspace
  [ -f "$workdir/anasd.pid" ] && [ -f "$workdir/anasd.path" ] || fail "daemon identity is missing"
  pid=$(sed -n '1p' "$workdir/anasd.pid")
  expected=$(sed -n '1p' "$workdir/anasd.path")
  pid_matches_daemon "$pid" "$expected" || fail "anasd is not running after browser workflow"

  list_document=$workdir/config-list.json
  if ! "$anas_bin" config list global -w "$workspace" --root "$module_root" --json >"$list_document"; then
    fail "read saved desired configuration"
  fi
  "$python_bin" - "$list_document" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1], encoding="utf-8"))
parameters = {item["path"]: item for item in body["parameters"]}
timezone = parameters["global.timezone"]
assert timezone["set"] is True, timezone
assert timezone["value"] == "Asia/Singapore", timezone
PY
  "$python_bin" - "$workspace/config.yml" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1], encoding="utf-8"))
modules = body.get("modules")
assert isinstance(modules, dict), body
assert "freeradius" in modules, modules
assert "freeradius" not in body, body
PY

  runtime_tree_digest "$workspace" >"$workdir/runtime-after.sha256"
  cmp -s "$workdir/runtime-before.sha256" "$workdir/runtime-after.sha256" ||
    fail "desired-config editing or saving changed deployments/data/userdata"

  printf '%s\n' 'PASS: CONSOLE-R-120 real daemon browser config workflow'
  printf '%s\n' 'desired_config=global.timezone:Asia/Singapore module:freeradius'
  printf '%s\n' 'runtime_tree=unchanged'
  printf '%s\n' 'daemon=running'
}

case "$command_name" in
  setup) setup_run ;;
  verify) verify_run ;;
  cleanup)
    require_root
    run_id=${ANAS_E2E_RUN_ID:-}
    workdir=$(workdir_for_run "$run_id")
    cleanup_run "$workdir"
    printf '%s\n' 'cleanup=complete'
    ;;
  *)
    printf 'usage: %s {setup|verify|cleanup}\n' "$0" >&2
    exit 2
    ;;
esac
