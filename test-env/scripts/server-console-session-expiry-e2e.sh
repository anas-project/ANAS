#!/usr/bin/env sh
# REQUIREMENTS: CONSOLE-R-185 CONSOLE-R-186
#
# Two claims cannot be proved by a unit test, because both are about what the
# browser does over real time against a real daemon:
#
#   R-186  The console estimates the remaining session locally and never polls
#          an authenticated route to refresh the countdown. A poll would slide
#          the server-side idle window, so the proof is a server-side one: after
#          the operator leaves an authenticated tab untouched, `idle_expires_at`
#          on the stored session must be byte-for-byte what it was when the tab
#          went idle, and the session must then expire on its own.
#   R-185  A session that stops being accepted returns the console to its entry
#          screen. `revoke` ends every local session server-side; the next thing
#          the operator touches must land on the login screen with the expired
#          message, without a manual reload.
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
umask 077

command_name=${1:-}
run_id=${ANAS_E2E_RUN_ID:-}
port=${ANAS_E2E_PORT:-7794}
lan_ip=${ANAS_E2E_LAN_IP:-}
anas_bin=${ANAS_BIN:-}
anasd_bin=${ANASD_BIN:-}
auth_fixture=${ANAS_AUTH_FIXTURE:-}
python_bin=${PYTHON:-$(command -v python3 || true)}
curl_bin=${CURL:-$(command -v curl || true)}
openssl_bin=${OPENSSL:-$(command -v openssl || true)}

fail() {
  printf 'console session expiry E2E failed: %s\n' "$1" >&2
  exit 1
}

require_root() {
  [ "$(id -u)" -eq 0 ] || fail "run as root so the console store uses production file policy"
}

validate_inputs() {
  case "$run_id" in
    ''|*[!A-Za-z0-9._-]*) fail "ANAS_E2E_RUN_ID must use only letters, digits, dot, underscore, or hyphen" ;;
  esac
  case "$port" in ''|*[!0-9]*) fail "ANAS_E2E_PORT must be numeric" ;; esac
  [ "$port" -ge 7700 ] && [ "$port" -le 7799 ] || fail "ANAS_E2E_PORT must be between 7700 and 7799"
  case "$lan_ip" in ''|*[!0-9.]*) fail "ANAS_E2E_LAN_IP must be a numeric IPv4 address the browser can reach" ;; esac
  for item in "$anas_bin" "$anasd_bin" "$auth_fixture"; do
    [ -n "$item" ] && [ -f "$item" ] && [ ! -L "$item" ] && [ -x "$item" ] ||
      fail "ANAS_BIN, ANASD_BIN and ANAS_AUTH_FIXTURE must name executable regular, non-symlink files"
  done
  for item in "$python_bin" "$curl_bin" "$openssl_bin"; do
    [ -n "$item" ] || fail "python3, curl and openssl are required"
  done
}

workdir_for_run() {
  printf '/tmp/anas-session-expiry.%s\n' "$run_id"
}

require_workdir() {
  directory=$(workdir_for_run)
  [ -d "$directory" ] || fail "test workdir does not exist: $directory"
}

pid_matches() {
  pid=$1
  expected=$2
  [ -n "$pid" ] && [ -d "/proc/$pid" ] || return 1
  [ "$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)" = "$(readlink -f "$expected")" ]
}

require_daemon() {
  directory=$1
  [ -f "$directory/daemon.pid" ] && [ -f "$directory/daemon.path" ] || fail "daemon identity is missing"
  pid=$(sed -n '1p' "$directory/daemon.pid")
  expected=$(sed -n '1p' "$directory/daemon.path")
  pid_matches "$pid" "$expected" || fail "the test daemon is not running"
}

wait_http() {
  url=$1
  directory=$2
  expected=$3
  attempt=0
  while :; do
    status=$($curl_bin -ksS -o "$directory/ready-body" -w '%{http_code}' "$url" 2>/dev/null || true)
    [ "$status" = "$expected" ] && return 0
    attempt=$((attempt + 1))
    [ "$attempt" -lt 200 ] || fail "timed out waiting for $url (last HTTP $status)"
    sleep 0.1
  done
}

report_sessions() {
  directory=$1
  target=$2
  "$auth_fixture" report-sessions "$directory/console-store" >"$target" ||
    fail "read local session records"
  chmod 0600 "$target"
}

setup_run() {
  require_root
  validate_inputs
  directory=$(workdir_for_run)
  [ ! -e "$directory" ] || fail "test workdir already exists: $directory"
  mkdir -m 0700 "$directory"

  tls_dir=$directory/tls
  mkdir -m 0700 "$tls_dir"
  {
    printf '%s\n' 'api_version: anas.console-config/v1'
    printf '%s\n' 'mode: lan'
    printf 'port: %s\n' "$port"
    printf 'console_store: %s\n' "$directory/console-store"
    printf '%s\n' 'tls:'
    printf '%s\n' '  temporary:'
    printf '    certificate: %s\n' "$tls_dir/temp-console.crt"
    printf '    private_key: %s\n' "$tls_dir/temp-console.key"
    printf '%s\n' '    ip_addresses:'
    printf '      - %s\n' "$lan_ip"
    printf '%s\n' '      - 127.0.0.1'
  } >"$directory/anasd.yml"
  chmod 0600 "$directory/anasd.yml"
  "$anas_bin" console tls --self-signed --config "$directory/anasd.yml" --ttl 30m --json \
    >"$directory/bootstrap-token.json" || fail "generate the temporary TLS material"

  owner_password=Session-Owner-$($openssl_bin rand -hex 18)-Aa1
  printf '%s\n' "$owner_password" >"$directory/owner-password"
  chmod 0600 "$directory/owner-password"
  ANAS_E2E_OWNER_PASSWORD=$owner_password "$auth_fixture" seed-full "$directory/console-store" \
    >"$directory/seed-full.json" || fail "seed a full-state console with a local owner"
  owner_password=
  chmod 0600 "$directory/seed-full.json"

  printf '%s\n' "$anasd_bin" >"$directory/daemon.path"
  nohup "$anasd_bin" --config "$directory/anasd.yml" </dev/null >"$directory/anasd.log" 2>&1 &
  printf '%s\n' "$!" >"$directory/daemon.pid"
  wait_http "https://127.0.0.1:$port/api/v1/system" "$directory" 200

  printf 'workdir=%s\n' "$directory"
  printf 'console_origin=https://%s:%s\n' "$lan_ip" "$port"
  printf 'owner_password_file=%s\n' "$directory/owner-password"
  printf '%s\n' 'ready=session_expiry_browser'
  printf '%s\n' 'next: sign in from the browser, leave the tab untouched, then run: snapshot'
}

# Run once the operator has signed in and is about to stop touching the tab.
snapshot_run() {
  require_root
  validate_inputs
  require_workdir
  directory=$(workdir_for_run)
  require_daemon "$directory"
  report_sessions "$directory" "$directory/sessions-idle-start.json"
  "$python_bin" - "$directory/sessions-idle-start.json" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1], encoding="utf-8"))
sessions = body["sessions"]
assert len(sessions) == 1, f"expected exactly one signed-in session, got {len(sessions)}"
print(f"session_digest={sessions[0]['digest'][:16]}")
print(f"idle_expires_at={sessions[0]['idle_expires_at']}")
print(f"expires_at={sessions[0]['expires_at']}")
PY
  printf 'idle_start_recorded=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf '%s\n' 'next: leave the tab open and untouched for at least 26 minutes, then run: verify-idle'
}

# The core R-186 assertion: an untouched authenticated tab must not have moved
# the server-side idle window, and the session must have expired on its own.
verify_idle_run() {
  require_root
  validate_inputs
  require_workdir
  directory=$(workdir_for_run)
  require_daemon "$directory"
  [ -f "$directory/sessions-idle-start.json" ] || fail "run snapshot before the idle wait"
  report_sessions "$directory" "$directory/sessions-idle-end.json"
  "$python_bin" - "$directory/sessions-idle-start.json" "$directory/sessions-idle-end.json" <<'PY'
import datetime
import json
import sys

start = json.load(open(sys.argv[1], encoding="utf-8"))["sessions"]
end = json.load(open(sys.argv[2], encoding="utf-8"))["sessions"]
assert len(start) == 1, start

recorded = start[0]
now = datetime.datetime.now(datetime.timezone.utc)
idle_deadline = datetime.datetime.fromisoformat(recorded["idle_expires_at"].replace("Z", "+00:00"))
assert now > idle_deadline, (
    "wait until the recorded idle deadline has passed before verifying; "
    f"{(idle_deadline - now).total_seconds():.0f}s remain"
)

# The record may already be gone: an expired session is dropped the next time
# the store is written. What must never happen is a record whose idle window
# moved forward, because only an authenticated request can do that and the
# operator made none.
survivors = [item for item in end if item["digest"] == recorded["digest"]]
for item in survivors:
    assert item["idle_expires_at"] == recorded["idle_expires_at"], (
        "the idle window slid while the tab was untouched: the console polled an "
        f"authenticated route ({recorded['idle_expires_at']} -> {item['idle_expires_at']})"
    )
    assert item["expires_at"] == recorded["expires_at"], item

print("idle_window_unchanged=true")
print(f"session_record_present={'true' if survivors else 'false'}")
PY
  printf '%s\n' 'PASS: CONSOLE-R-186 the idle window did not slide while the console was idle'
  printf '%s\n' 'next: confirm the browser returned to the login screen on its own, then run: record'
}

# The R-185 path that needs no waiting: end every session server-side and watch
# what the already-open console does with its next request.
revoke_run() {
  require_root
  validate_inputs
  require_workdir
  directory=$(workdir_for_run)
  require_daemon "$directory"
  "$auth_fixture" revoke-sessions "$directory/console-store" >"$directory/revoke.json" ||
    fail "revoke local sessions"
  chmod 0600 "$directory/revoke.json"
  report_sessions "$directory" "$directory/sessions-after-revoke.json"
  "$python_bin" - "$directory/sessions-after-revoke.json" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1], encoding="utf-8"))
assert body["sessions"] == [], body
print("local_sessions_remaining=0")
PY
  printf '%s\n' 'PASS: every local session was revoked while the console stayed open'
  printf '%s\n' 'next: interact with the open console once, then run: record'
}

# The browser half cannot be asserted from the server, so it is recorded in the
# operator's own words and carried into the plan's e2e table verbatim.
record_run() {
  require_root
  validate_inputs
  require_workdir
  directory=$(workdir_for_run)
  observation=${1:-}
  [ -n "$observation" ] || fail "pass the observed browser behaviour as one argument"
  printf '%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$observation" >>"$directory/browser-observations.tsv"
  chmod 0600 "$directory/browser-observations.tsv"
  printf 'recorded=%s\n' "$observation"
}

verify_run() {
  require_root
  validate_inputs
  require_workdir
  directory=$(workdir_for_run)
  require_daemon "$directory"
  [ -f "$directory/sessions-idle-end.json" ] || fail "run verify-idle before verify"
  [ -f "$directory/sessions-after-revoke.json" ] || fail "run revoke before verify"
  [ -s "$directory/browser-observations.tsv" ] || fail "record the observed browser behaviour before verify"
  report_sessions "$directory" "$directory/sessions-final.json"
  "$python_bin" - "$directory/sessions-final.json" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1], encoding="utf-8"))
assert body["sessions"] == [], body
print("local_sessions_remaining=0")
PY
  printf '%s\n' 'PASS: CONSOLE-R-185 CONSOLE-R-186 console session expiry E2E'
  printf 'observations=%s\n' "$directory/browser-observations.tsv"
  printf '%s\n' 'daemon=running'
}

cleanup_run() {
  require_root
  validate_inputs
  directory=$(workdir_for_run)
  [ -d "$directory" ] || return 0
  if [ -f "$directory/daemon.pid" ] && [ -f "$directory/daemon.path" ]; then
    pid=$(sed -n '1p' "$directory/daemon.pid")
    expected=$(sed -n '1p' "$directory/daemon.path")
    if pid_matches "$pid" "$expected"; then
      kill "$pid" 2>/dev/null || true
      count=0
      while pid_matches "$pid" "$expected" && [ "$count" -lt 100 ]; do
        sleep 0.1
        count=$((count + 1))
      done
      pid_matches "$pid" "$expected" && kill -KILL "$pid" 2>/dev/null || true
    fi
  fi
  rm -rf "$directory"
  printf 'cleaned=%s\n' "$directory"
}

case "$command_name" in
  setup) setup_run ;;
  snapshot) snapshot_run ;;
  verify-idle) verify_idle_run ;;
  revoke) revoke_run ;;
  record) shift; record_run "$@" ;;
  verify) verify_run ;;
  cleanup) cleanup_run ;;
  *)
    printf 'usage: %s {setup|snapshot|verify-idle|revoke|record OBSERVATION|verify|cleanup}\n' "$0" >&2
    exit 2
    ;;
esac
