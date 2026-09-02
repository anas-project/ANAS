#!/usr/bin/env sh
# REQUIREMENTS: CONSOLE-R-059
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

anas_bin=${ANAS_BIN:-}
anasd_bin=${ANASD_BIN:-}
fixture_bin=${ANAS_JOB_FIXTURE_BIN:-}
python_bin=${PYTHON:-$(command -v python3 || true)}
curl_bin=${CURL:-$(command -v curl || true)}
daemon_log=
daemon_pid=
loop_device=
mounted=false
stage=preflight
failure_reported=false

fail() {
  failure_reported=true
  printf 'R-059 E2E failed: %s\n' "$1" >&2
  if [ -n "${daemon_log:-}" ] && [ -f "$daemon_log" ]; then
    printf '%s\n' '--- anasd log ---' >&2
    tail -n 120 "$daemon_log" >&2 || true
  fi
  exit 1
}

if [ "$(id -u)" -ne 0 ]; then
  fail "run as root so the test can mount an isolated Btrfs loop filesystem and enforce production file policy"
fi
for item in "$anas_bin" "$anasd_bin" "$fixture_bin"; do
  [ -n "$item" ] && [ -f "$item" ] && [ ! -L "$item" ] && [ -x "$item" ] ||
    fail "ANAS_BIN, ANASD_BIN and ANAS_JOB_FIXTURE_BIN must name executable regular files"
done
[ -n "$python_bin" ] || fail "python3 is required"
[ -n "$curl_bin" ] || fail "curl is required"
for command_name in mkfs.btrfs losetup mount umount mountpoint truncate sha256sum; do
  command -v "$command_name" >/dev/null 2>&1 || fail "$command_name is required"
done

workdir=$(mktemp -d /tmp/anas-r059.XXXXXX)
mount_root=$workdir/btrfs
image=$workdir/btrfs.img
workspace=$mount_root/workspace
restore_workspace=$mount_root/restored
console_store=$workdir/console-store
policy_store=$workdir/policy-store
backup_dir=$workdir/backups
bundle_root=$workdir/module-bundle
module_root=$bundle_root/modules
service_config=$workdir/anasd.yml
cookie_jar=$workdir/bootstrap-cookies.txt
daemon_log=$workdir/anasd.log

stop_daemon() {
  if [ -n "$daemon_pid" ] && kill -0 "$daemon_pid" 2>/dev/null; then
    kill "$daemon_pid" 2>/dev/null || true
    count=0
    while kill -0 "$daemon_pid" 2>/dev/null && [ "$count" -lt 100 ]; do
      sleep 0.1
      count=$((count + 1))
    done
    if kill -0 "$daemon_pid" 2>/dev/null; then
      kill -KILL "$daemon_pid" 2>/dev/null || true
    fi
    wait "$daemon_pid" 2>/dev/null || true
  fi
  daemon_pid=
}

cleanup() {
  cleanup_status=$?
  trap - EXIT HUP INT TERM
  stop_daemon
  if [ -n "${workspace:-}" ] && [ -d "$workspace/.anas" ]; then
    "$anas_bin" stop -w "$workspace" >/dev/null 2>&1 || true
  fi
  if [ -n "${restore_workspace:-}" ] && [ -d "$restore_workspace/.anas" ]; then
    "$anas_bin" stop -w "$restore_workspace" >/dev/null 2>&1 || true
  fi
  if [ "$cleanup_status" -ne 0 ] && [ "$failure_reported" != true ]; then
    printf 'R-059 E2E failed unexpectedly during stage: %s (exit %s)\n' "$stage" "$cleanup_status" >&2
    if [ -n "${daemon_log:-}" ] && [ -f "$daemon_log" ]; then
      printf '%s\n' '--- anasd log ---' >&2
      tail -n 120 "$daemon_log" >&2 || true
    fi
  fi
  if [ "$mounted" = true ] && mountpoint -q "$mount_root"; then
    sync || true
    umount "$mount_root" || true
  fi
  if [ -n "$loop_device" ]; then
    losetup -d "$loop_device" 2>/dev/null || true
  fi
  case "$workdir" in
    /tmp/anas-r059.*) rm -rf -- "$workdir" ;;
    *) printf 'refusing to clean unexpected path: %s\n' "$workdir" >&2 ;;
  esac
  exit "$cleanup_status"
}
trap cleanup EXIT HUP INT TERM

rewrite_module_view() {
  target=$1
  "$python_bin" - "$target/.anas/module-view.json" "$module_root" <<'PY'
import json
import os
import sys

target, module_root = sys.argv[1:]
with open(target, "w", encoding="utf-8") as stream:
    json.dump({
        "api_version": "anas.module-view/v1",
        "digest": "r059-e2e",
        "module_root": os.path.abspath(module_root),
        "installations": {},
    }, stream, indent=2)
    stream.write("\n")
os.chmod(target, 0o600)
PY
}

json_field() {
  "$python_bin" -c '
import json
import sys

value = json.load(open(sys.argv[1]))
for component in sys.argv[2].split("."):
    value = value[component]
print(value)
' "$1" "$2"
}

assert_tree_excludes_console() {
  tree=$1
  marker_one=$2
  marker_two=$3
  "$python_bin" - "$tree" "$marker_one" "$marker_two" <<'PY'
import os
import sys

root = os.path.abspath(sys.argv[1])
markers = [value.encode() for value in sys.argv[2:] if value]
for directory, names, files in os.walk(root, followlinks=False):
    relative = os.path.relpath(directory, root)
    components = [] if relative == "." else relative.split(os.sep)
    assert "console-store" not in components, directory
    for name in files:
        path = os.path.join(directory, name)
        if os.path.islink(path) or not os.path.isfile(path):
            continue
        with open(path, "rb") as stream:
            body = stream.read()
        for marker in markers:
            assert marker not in body, path
PY
}

stage=btrfs_setup
mkdir -m 0700 "$mount_root"
truncate -s 512M "$image"
loop_device=$(losetup --find --show "$image")
mkfs.btrfs -q -f "$loop_device"
mount -t btrfs -o user_subvol_rm_allowed "$loop_device" "$mount_root"
mounted=true

stage=workspace_setup
mkdir -p "$module_root/r059_fixture" "$bundle_root/contracts"
chmod 0700 "$bundle_root" "$module_root" "$module_root/r059_fixture" "$bundle_root/contracts"
cat >"$module_root/r059_fixture/module.yml" <<'EOF'
api_version: anas.module/v1
kind: Module
name: r059_fixture
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
EOF
chmod 0600 "$module_root/r059_fixture/module.yml"
"$anas_bin" init "$workspace" -y >/dev/null 2>&1 || fail "initialize source workspace"
rewrite_module_view "$workspace"
minimal_config=$workdir/minimal-config.yml
cat >"$minimal_config" <<'EOF'
module_source: official
modules:
  r059_fixture: {}
global:
  base_domain: example.test
  email: admin@example.test
  timezone: UTC
env:
  CONTAINER_PREFIX: anas_r059_
  NETWORK_PREFIX: anas_r059_
EOF
chmod 0600 "$minimal_config"
if ! "$anas_bin" config import "$minimal_config" -w "$workspace" --root "$module_root" --json \
  >"$workdir/config-import.json" 2>"$workdir/config-import.log"; then
  sed -n '1,120p' "$workdir/config-import.json" >&2 || true
  sed -n '1,120p' "$workdir/config-import.log" >&2 || true
  fail "import minimal source configuration"
fi
if ! "$anas_bin" apply --update-lock -w "$workspace" --root "$module_root" -y --json \
  >"$workdir/apply.json" 2>"$workdir/apply.log"; then
  sed -n '1,160p' "$workdir/apply.json" >&2 || true
  sed -n '1,160p' "$workdir/apply.log" >&2 || true
  fail "create an active deployment for snapshot/backup"
fi
[ "$(stat -c %i "$workspace/data")" = 256 ] || fail "source data directory is not a Btrfs subvolume"
printf '%s\n' before-snapshot >"$workspace/data/r059-before"

port=$(
  "$python_bin" - <<'PY'
import socket

sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()
PY
)
origin=http://127.0.0.1:$port
mkdir -m 0700 "$console_store" "$policy_store" "$backup_dir"
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

stage=storage_compaction
token_document=$("$anas_bin" console token --config "$service_config" --ttl 15m --json) ||
  fail "issue bootstrap token"
bootstrap_token=$(printf '%s' "$token_document" | "$python_bin" -c 'import json,sys; print(json.load(sys.stdin)["token"])')
transaction_id=$(printf '%s' "$token_document" | "$python_bin" -c 'import json,sys; print(json.load(sys.stdin)["transaction_id"])')
[ -n "$bootstrap_token" ] && [ -n "$transaction_id" ] || fail "bootstrap token output was incomplete"
fixture_result=$workdir/fixture.json
"$fixture_bin" seed-pruned "$policy_store" "$transaction_id" "$policy_store" >"$fixture_result" ||
  fail "seed independently bounded job/audit journals"
"$python_bin" - "$fixture_result" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1]))
assert body["job_event_capacity"] == 1, body
assert body["audit_max_events"] == 2, body
assert body["retained_job_events"] == 1, body
assert body["retained_audit_events"] == 2, body
assert body["job_bytes_after_compact"] < body["job_bytes_before_compact"], body
assert body["audit_bytes_after_compact"] < body["audit_bytes_before_compact"], body
assert body["pruned_through"] > 0, body
assert body["oldest_available"] == body["latest_id"], body
PY
install -m 0600 "$policy_store/jobs.jsonl" "$console_store/jobs.jsonl"
install -m 0600 "$policy_store/jobs.lock" "$console_store/jobs.lock"
job_id=$(json_field "$fixture_result" job_id)

stage=http_event_gap
"$anasd_bin" --config "$service_config" >"$daemon_log" 2>&1 &
daemon_pid=$!
csrf_body=$workdir/bootstrap-csrf.json
attempt=0
while :; do
  ready_status=$("$curl_bin" -sS -c "$cookie_jar" -o "$csrf_body" -w '%{http_code}' \
    "$origin/api/v1/auth/csrf" 2>/dev/null || true)
  [ "$ready_status" = 200 ] && break
  kill -0 "$daemon_pid" 2>/dev/null || fail "anasd exited before accepting bootstrap requests"
  attempt=$((attempt + 1))
  [ "$attempt" -lt 100 ] || fail "anasd did not become ready"
  sleep 0.1
done
preauth_csrf=$(json_field "$csrf_body" csrf_token)
exchange_request=$workdir/bootstrap-exchange.json
BOOTSTRAP_TOKEN=$bootstrap_token "$python_bin" - "$exchange_request" <<'PY'
import json
import os
import sys

with open(sys.argv[1], "w", encoding="utf-8") as stream:
    json.dump({"token": os.environ["BOOTSTRAP_TOKEN"]}, stream, separators=(",", ":"))
PY
chmod 0600 "$exchange_request"
exchange_status=$("$curl_bin" -sS -b "$cookie_jar" -c "$cookie_jar" -o "$workdir/bootstrap-exchange-response.json" -w '%{http_code}' \
  -H "Origin: $origin" -H "X-CSRF-Token: $preauth_csrf" -H 'Content-Type: application/json' \
  --data-binary "@$exchange_request" "$origin/api/v1/auth/bootstrap/exchange") ||
  fail "bootstrap exchange transport failed"
[ "$exchange_status" = 200 ] || fail "bootstrap exchange returned HTTP $exchange_status"
bootstrap_token=
token_document=
: >"$exchange_request"
gap_body=$workdir/event-gap.json
gap_status=$("$curl_bin" -sS -b "$cookie_jar" -o "$gap_body" -w '%{http_code}' \
  -H 'Accept: text/event-stream' -H 'Last-Event-ID: 0' "$origin/api/v1/jobs/$job_id/events") ||
  fail "event-gap request transport failed"
[ "$gap_status" = 410 ] || fail "expired Last-Event-ID returned HTTP $gap_status"
FIXTURE_RESULT=$fixture_result "$python_bin" - "$gap_body" "$job_id" <<'PY'
import json
import os
import sys

gap = json.load(open(sys.argv[1]))
fixture = json.load(open(os.environ["FIXTURE_RESULT"]))
assert gap["status"] == 410 and gap["code"] == "event_gap", gap
assert gap["job_id"] == sys.argv[2], gap
assert gap["requested_after"] == 0, gap
for key in ("pruned_through", "oldest_available", "latest_id"):
    assert gap[key] == fixture[key], (gap, fixture)
PY
stop_daemon

stage=snapshot_isolation
sentinel_one=r059-console-store-before-snapshot-$transaction_id
printf '%s\n' "$sentinel_one" >"$console_store/r059-sentinel"
chmod 0600 "$console_store/r059-sentinel"
snapshot_result=$workdir/snapshot.json
"$anas_bin" snapshot create -w "$workspace" --label r059-console-isolation --json >"$snapshot_result" ||
  fail "create R-059 snapshot"
snapshot_id=$(json_field "$snapshot_result" snapshot.id)
[ -n "$snapshot_id" ] || fail "snapshot output omitted id"
snapshot_root=$workspace/snapshots/$snapshot_id
assert_tree_excludes_console "$snapshot_root" "$sentinel_one" ""
printf '%s\n' after-snapshot >"$workspace/data/r059-after"
sentinel_two=r059-console-store-before-snapshot-restore-$transaction_id
printf '%s\n' "$sentinel_two" >"$console_store/r059-sentinel"
sentinel_digest=$(sha256sum "$console_store/r059-sentinel" | awk '{print $1}')
"$anas_bin" snapshot restore "$snapshot_id" -w "$workspace" -y --json >"$workdir/snapshot-restore.json" ||
  fail "restore source snapshot"
[ -f "$workspace/data/r059-before" ] || fail "snapshot restore lost the captured data marker"
[ ! -e "$workspace/data/r059-after" ] || fail "snapshot restore did not rewind source data"
[ "$(sha256sum "$console_store/r059-sentinel" | awk '{print $1}')" = "$sentinel_digest" ] ||
  fail "snapshot restore overwrote console store"

stage=backup_isolation
backup_result=$workdir/backup.json
"$anas_bin" backup create --to "$backup_dir" --mode copy --snapshot "$snapshot_id" \
  --skip-userdata -w "$workspace" -y --json >"$backup_result" || fail "create copy backup from snapshot"
backup_id=$(json_field "$backup_result" backup_id)
[ -n "$backup_id" ] || fail "backup output omitted backup_id"
backup_root=$backup_dir/$backup_id
assert_tree_excludes_console "$backup_root" "$sentinel_one" "$sentinel_two"
"$anas_bin" backup verify --to "$backup_dir" --backup-id "$backup_id" --json >"$workdir/backup-verify.json" ||
  fail "verify R-059 backup"

stage=backup_restore_isolation
"$anas_bin" init "$restore_workspace" -y >/dev/null 2>&1 || fail "initialize restore workspace"
rewrite_module_view "$restore_workspace"
sentinel_three=r059-console-store-before-backup-restore-$transaction_id
printf '%s\n' "$sentinel_three" >"$console_store/r059-sentinel"
sentinel_digest=$(sha256sum "$console_store/r059-sentinel" | awk '{print $1}')
"$anas_bin" backup restore --from "$backup_dir" --backup-id "$backup_id" \
  -w "$restore_workspace" -y --json >"$workdir/backup-restore.json" || fail "restore R-059 backup"
[ -f "$restore_workspace/data/r059-before" ] || fail "backup restore lost the captured data marker"
[ ! -e "$restore_workspace/data/r059-after" ] || fail "backup restore included post-snapshot data"
[ "$(sha256sum "$console_store/r059-sentinel" | awk '{print $1}')" = "$sentinel_digest" ] ||
  fail "backup restore overwrote console store"
assert_tree_excludes_console "$restore_workspace" "$sentinel_one" "$sentinel_two"

stage=completed
printf 'environment=%s %s workspace_fs=%s backing_fs=%s\n' \
  "$(. /etc/os-release; printf '%s-%s' "$ID" "$VERSION_ID")" "$(uname -m)" \
  "$(stat -f -c %T "$workspace")" "$(stat -f -c %T "$workdir")"
printf 'R-059 job_capacity=1 retained_job_events=1 job_bytes=%s/%s audit_max_events=2 retained_audit_events=2 audit_bytes=%s/%s event_gap_http=%s pruned_through=%s latest_id=%s\n' \
  "$(json_field "$fixture_result" job_bytes_before_compact)" "$(json_field "$fixture_result" job_bytes_after_compact)" \
  "$(json_field "$fixture_result" audit_bytes_before_compact)" "$(json_field "$fixture_result" audit_bytes_after_compact)" \
  "$gap_status" "$(json_field "$fixture_result" pruned_through)" "$(json_field "$fixture_result" latest_id)"
printf 'R-059 snapshot=%s backup=%s console_store_preserved=snapshot-create/snapshot-restore/backup-create/backup-restore\n' \
  "$snapshot_id" "$backup_id"
printf '%s\n' 'PASS: CONSOLE-R-059 physically compacts independent journals, reports replay gaps, and isolates console store from workspace recovery'
