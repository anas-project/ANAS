#!/usr/bin/env sh
# REQUIREMENTS: CONSOLE-R-125 CONSOLE-R-126
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
umask 077

command_name=${1:-}
run_id=${ANAS_E2E_RUN_ID:-}
job_id=${ANAS_E2E_JOB_ID:-}
job_fixture=${ANAS_JOB_FIXTURE:-}
module_root=${ANAS_MODULE_ROOT:-}
python_bin=${PYTHON:-$(command -v python3 || true)}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
config_ui_e2e=$script_dir/server-console-config-ui-e2e.sh

fail() {
  printf 'R-125 job refresh E2E failed: %s\n' "$1" >&2
  exit 1
}

require_root() {
  [ "$(id -u)" -eq 0 ] || fail "run as root so the job store uses production file policy"
}

validate_run_id() {
  case "$1" in
    ''|*[!A-Za-z0-9._-]*) fail "ANAS_E2E_RUN_ID must use only letters, digits, dot, underscore, or hyphen" ;;
  esac
}

require_regular_executable() {
  [ -n "$1" ] && [ -f "$1" ] && [ ! -L "$1" ] && [ -x "$1" ] ||
    fail "$2 must name an executable regular, non-symlink file"
}

verify_run() {
  require_root
  validate_run_id "$run_id"
  [ -n "$job_id" ] || fail "ANAS_E2E_JOB_ID must be the exact job shown before and after browser refresh"
  require_regular_executable "$job_fixture" ANAS_JOB_FIXTURE
  require_regular_executable "$config_ui_e2e" CONFIG_UI_E2E_SCRIPT
  [ -n "$python_bin" ] || fail "python3 is required"

  workdir=/tmp/anas-r120.$run_id
  [ -d "$workdir" ] || fail "test workdir does not exist: $workdir"
  [ -f "$workdir/anasd.pid" ] && [ -f "$workdir/anasd.path" ] || fail "daemon identity is missing"
  daemon_pid=$(sed -n '1p' "$workdir/anasd.pid")
  daemon_path=$(sed -n '1p' "$workdir/anasd.path")
  [ -d "/proc/$daemon_pid" ] || fail "anasd is not running after browser refresh"
  actual_path=$(readlink -f "/proc/$daemon_pid/exe" 2>/dev/null || true)
  [ "$actual_path" = "$(readlink -f "$daemon_path")" ] || fail "recorded PID is not the test daemon"

  job_document=$workdir/r125-job.json
  if ! "$job_fixture" inspect "$workdir/console-store" "$job_id" >"$job_document"; then
    fail "read the browser-observed job from the durable store"
  fi
  "$python_bin" - "$job_document" "$job_id" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1], encoding="utf-8"))
expected_id = sys.argv[2]
assert body["id"] == expected_id, body
assert body["kind"] == "deployment.plan", body
assert body["workspace_id"] == "main", body
assert body["mutating"] is False, body
assert body["status"] == "succeeded", body
assert body["progress"] == 100, body
assert body["created_by"].startswith("bootstrap:"), body
assert body["revision"] >= 2, body
PY
  chmod 0600 "$job_document"

  printf '%s\n' 'PASS: CONSOLE-R-125 browser refresh restored durable task drawer'
  printf 'job_id=%s\n' "$job_id"
  printf '%s\n' 'job_kind=deployment.plan'
  printf '%s\n' 'job_status=succeeded'
  printf '%s\n' 'daemon=running'
}

case "$command_name" in
  setup)
    require_regular_executable "$config_ui_e2e" CONFIG_UI_E2E_SCRIPT
    [ -n "$module_root" ] && [ -d "$module_root" ] && [ ! -L "$module_root" ] ||
      fail "ANAS_MODULE_ROOT must name an unpacked Module directory"
    contract_root=$(dirname -- "$module_root")/contracts
    [ -d "$contract_root" ] && [ ! -L "$contract_root" ] ||
      fail "the R-125 plan fixture requires contracts beside ANAS_MODULE_ROOT"
    "$config_ui_e2e" setup
    printf '%s\n' 'ready=browser_job_refresh'
    ;;
  verify) verify_run ;;
  cleanup)
    require_regular_executable "$config_ui_e2e" CONFIG_UI_E2E_SCRIPT
    "$config_ui_e2e" cleanup
    ;;
  *)
    printf 'usage: %s {setup|verify|cleanup}\n' "$0" >&2
    exit 2
    ;;
esac
