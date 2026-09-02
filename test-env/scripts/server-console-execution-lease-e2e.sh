#!/usr/bin/env sh
# REQUIREMENTS: CONSOLE-R-049
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

anasd_bin=${ANASD_BIN:-}
fixture_bin=${ANAS_JOB_FIXTURE_BIN:-}
python_bin=${PYTHON:-$(command -v python3 || true)}
curl_bin=${CURL:-$(command -v curl || true)}
daemon_log=

fail() {
  printf 'R-049 E2E failed: %s\n' "$1" >&2
  for log in ${daemon_log:-}; do
    if [ -f "$log" ]; then
      printf '%s\n' "--- $(basename "$log") ---" >&2
      tail -n 100 "$log" >&2 || true
    fi
  done
  exit 1
}

if [ "$(id -u)" -ne 0 ]; then
  fail "run as root so anasd can enforce its production service-config policy"
fi
for item in "$anasd_bin" "$fixture_bin"; do
  [ -n "$item" ] && [ -f "$item" ] && [ ! -L "$item" ] && [ -x "$item" ] ||
    fail "ANASD_BIN and ANAS_JOB_FIXTURE_BIN must name executable regular files"
done
[ -n "$python_bin" ] || fail "python3 is required"
[ -n "$curl_bin" ] || fail "curl is required"

workdir=$(mktemp -d /tmp/anas-r049.XXXXXX)
workspace=$workdir/workspace
console_store=$workdir/console-store
service_config=$workdir/anasd.yml
first_log=$workdir/anasd-first.log
second_log=$workdir/anasd-contender.log
recovery_log=$workdir/anasd-recovery.log
daemon_log="$first_log $second_log $recovery_log"
first_pid=
second_pid=
recovery_pid=

stop_process() {
  target_pid=$1
  if [ -n "$target_pid" ] && kill -0 "$target_pid" 2>/dev/null; then
    kill "$target_pid" 2>/dev/null || true
    count=0
    while kill -0 "$target_pid" 2>/dev/null && [ "$count" -lt 100 ]; do
      sleep 0.1
      count=$((count + 1))
    done
    if kill -0 "$target_pid" 2>/dev/null; then
      kill -KILL "$target_pid" 2>/dev/null || true
    fi
    wait "$target_pid" 2>/dev/null || true
  fi
}

cleanup() {
  stop_process "$second_pid"
  stop_process "$recovery_pid"
  stop_process "$first_pid"
  case "$workdir" in
    /tmp/anas-r049.*) rm -rf -- "$workdir" ;;
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
mkdir -p "$workspace/.anas"
chmod 0700 "$workspace/.anas"
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

wait_ready() {
  target_pid=$1
  attempts=0
  while :; do
    status=$("$curl_bin" -sS -o /dev/null -w '%{http_code}' "$origin/api/v1/auth/csrf" 2>/dev/null || true)
    [ "$status" = 200 ] && return 0
    kill -0 "$target_pid" 2>/dev/null || fail "anasd exited before accepting HTTP requests"
    attempts=$((attempts + 1))
    [ "$attempts" -lt 100 ] || fail "anasd did not become ready"
    sleep 0.1
  done
}

file_digest() {
  "$python_bin" -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$1"
}

assert_job() {
  document=$1
  expected_status=$2
  expected_compensation=$3
  DOCUMENT=$document EXPECTED_STATUS=$expected_status EXPECTED_COMPENSATION=$expected_compensation \
    "$python_bin" - <<'PY'
import json
import os

job = json.loads(os.environ["DOCUMENT"])
assert job["status"] == os.environ["EXPECTED_STATUS"], job
assert job["needs_compensation_check"] is (os.environ["EXPECTED_COMPENSATION"] == "true"), job
if job["status"] == "interrupted":
    assert job["error"]["code"] == "daemon_restarted", job
PY
}

"$anasd_bin" --config "$service_config" >"$first_log" 2>&1 &
first_pid=$!
wait_ready "$first_pid"

running_document=$("$fixture_bin" seed-running "$console_store") || fail "seed running job"
job_id=$(DOCUMENT=$running_document "$python_bin" -c 'import json,os; print(json.loads(os.environ["DOCUMENT"])["id"])')
[ -n "$job_id" ] || fail "running fixture omitted job ID"
assert_job "$running_document" running false

journal=$console_store/jobs.jsonl
journal_before_reader=$(file_digest "$journal")
reader_document=$("$fixture_bin" inspect "$console_store" "$job_id") || fail "open job store as an independent reader"
assert_job "$reader_document" running false
journal_after_reader=$(file_digest "$journal")
[ "$journal_after_reader" = "$journal_before_reader" ] || fail "ordinary Store open modified a running job"

"$anasd_bin" --config "$service_config" >"$second_log" 2>&1 &
second_pid=$!
second_status=0
wait "$second_pid" || second_status=$?
second_pid=
[ "$second_status" -ne 0 ] || fail "second daemon unexpectedly acquired the execution lease"
grep -F 'acquire console job execution lease' "$second_log" >/dev/null ||
  fail "second daemon did not report execution-lease contention"
kill -0 "$first_pid" 2>/dev/null || fail "lease holder exited while contender waited"
journal_after_contender=$(file_digest "$journal")
[ "$journal_after_contender" = "$journal_before_reader" ] || fail "contending daemon modified the active job journal"
contended_document=$("$fixture_bin" inspect "$console_store" "$job_id") || fail "inspect job after daemon contention"
assert_job "$contended_document" running false

stop_process "$first_pid"
first_pid=
"$anasd_bin" --config "$service_config" >"$recovery_log" 2>&1 &
recovery_pid=$!
wait_ready "$recovery_pid"
recovered_document=$("$fixture_bin" inspect "$console_store" "$job_id") || fail "inspect recovered job"
assert_job "$recovered_document" interrupted true
grep -F 'daemon_restarted' "$console_store/audit.jsonl" >/dev/null ||
  fail "restart recovery did not persist the deployment audit reason"

printf 'environment=%s %s filesystem=%s\n' \
  "$(. /etc/os-release; printf '%s-%s' "$ID" "$VERSION_ID")" \
  "$(uname -m)" "$(stat -f -c %T "$workdir")"
printf 'R-049 reader_preserved_running=yes contender_exit=%s contender_preserved_running=yes restart_status=interrupted compensation_required=yes audit_reason=daemon_restarted\n' \
  "$second_status"
printf '%s\n' 'PASS: CONSOLE-R-049 execution lease gates daemon recovery and preserves active jobs'
