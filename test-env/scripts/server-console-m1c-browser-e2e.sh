#!/usr/bin/env sh
# REQUIREMENTS: CONSOLE-R-103 CONSOLE-R-104 CONSOLE-R-113 CONSOLE-R-126 CONSOLE-R-128 CONSOLE-R-129 CONSOLE-R-130 CONSOLE-R-131 CONSOLE-R-156
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
umask 077

command_name=${1:-}
run_id=${ANAS_E2E_RUN_ID:-}
port=${ANAS_E2E_PORT:-}
fault_port=${ANAS_E2E_FAULT_PORT:-}
lan_ip=${ANAS_E2E_LAN_IP:-}
anas_bin=${ANAS_BIN:-}
anasd_bin=${ANASD_BIN:-}
auth_fixture=${ANAS_AUTH_FIXTURE_BIN:-}
jobs_fixture=${ANAS_JOB_FIXTURE:-}
fault_proxy=${ANAS_WEB_FAULT_PROXY_BIN:-}
module_root=${ANAS_MODULE_ROOT:-}
python_bin=${PYTHON:-$(command -v python3 || true)}
curl_bin=${CURL:-$(command -v curl || true)}
docker_bin=${DOCKER:-$(command -v docker || true)}
openssl_bin=${OPENSSL:-$(command -v openssl || true)}
iam_image=${ANAS_E2E_IAM_IMAGE:-casbin/casdoor-all-in-one:3.164.0}

fail() {
  printf 'M1C browser E2E failed: %s\n' "$1" >&2
  exit 1
}

require_root() {
  [ "$(id -u)" -eq 0 ] || fail "run as root so anasd and workspace state use production policy"
}

validate_inputs() {
  case "$run_id" in
    ''|*[!A-Za-z0-9._-]*) fail "ANAS_E2E_RUN_ID must use only letters, digits, dot, underscore, or hyphen" ;;
  esac
  for value in "$port" "$fault_port"; do
    case "$value" in ''|*[!0-9]*) fail "E2E ports must be numeric" ;; esac
    [ "$value" -ge 7700 ] && [ "$value" -le 7799 ] || fail "E2E ports must be between 7700 and 7799"
  done
  [ "$port" != "$fault_port" ] || fail "daemon and fault-proxy ports must differ"
  case "$lan_ip" in ''|*[!0-9.]*) fail "ANAS_E2E_LAN_IP must be a numeric IPv4 address" ;; esac
  for item in "$anas_bin" "$anasd_bin" "$auth_fixture" "$jobs_fixture" "$fault_proxy"; do
    [ -n "$item" ] && [ -f "$item" ] && [ ! -L "$item" ] && [ -x "$item" ] ||
      fail "all ANAS binaries and E2E fixtures must be executable regular files"
  done
  [ -n "$module_root" ] && [ -d "$module_root" ] && [ ! -L "$module_root" ] || fail "ANAS_MODULE_ROOT must be an unpacked directory"
  [ -d "$(dirname -- "$module_root")/contracts" ] || fail "contracts must be beside ANAS_MODULE_ROOT"
  for item in "$python_bin" "$curl_bin" "$docker_bin" "$openssl_bin"; do
    [ -n "$item" ] || fail "python3, curl, docker and openssl are required"
  done
}

workdir_for_run() {
  printf '/tmp/anas-m1c.%s\n' "$run_id"
}

pid_matches() {
  pid=$1
  expected=$2
  [ -n "$pid" ] && [ -d "/proc/$pid" ] || return 1
  [ "$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)" = "$(readlink -f "$expected")" ]
}

stop_process() {
  directory=$1
  label=$2
  [ -f "$directory/$label.pid" ] && [ -f "$directory/$label.path" ] || return 0
  pid=$(sed -n '1p' "$directory/$label.pid")
  expected=$(sed -n '1p' "$directory/$label.path")
  if pid_matches "$pid" "$expected"; then
    kill "$pid" 2>/dev/null || true
    count=0
    while pid_matches "$pid" "$expected" && [ "$count" -lt 100 ]; do
      sleep 0.1
      count=$((count + 1))
    done
    pid_matches "$pid" "$expected" && kill -KILL "$pid" 2>/dev/null || true
  fi
}

start_daemon() {
  directory=$1
  printf '%s\n' "$anasd_bin" >"$directory/daemon.path"
  nohup "$anasd_bin" --config "$directory/anasd.yml" </dev/null >"$directory/anasd.log" 2>&1 &
  printf '%s\n' "$!" >"$directory/daemon.pid"
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

write_fixture_module() {
  directory=$1
  fixture_root=$directory/catalog/modules
  fixture_module=$fixture_root/m1c_fixture
  mkdir -p "$fixture_module"
  cp -a "$(dirname -- "$module_root")/contracts" "$directory/catalog/contracts"
  cat >"$fixture_module/module.yml" <<'YAML'
api_version: anas.module/v1
kind: Module
name: m1c_fixture
version: 1.0.0
revision: 1
app_version: 1.0.0
abi:
  supports: [anas.module-hook/v1]
title: M1C browser fixture
description: Isolated guarded-change browser fixture.
category: testing
status: release
runtime:
  type: compose
  compose_file: docker-compose.yml
upgrade:
  data_breaking: []
config:
  defaults:
    marker: baseline
  types:
    marker: string
  changes:
    marker:
      effect: data_migrate
      apply: migrate-m1c-fixture-marker
      description: Browser E2E guarded-change marker.
YAML
  cat >"$fixture_module/docker-compose.yml" <<'YAML'
services:
  anas_m1c_fixture:
    image: docker.io/library/busybox:1.37.0
    restart: unless-stopped
    command: ["sh", "-c", "trap 'exit 0' TERM INT; while :; do sleep 3600; done"]
YAML
  printf '%s\n' "$fixture_root"
}

verify_risky_jobs() {
  directory=$1
  principal_kind=$2
  jobs_json=$directory/jobs-$principal_kind.json
  "$jobs_fixture" list "$directory/console-store" >"$jobs_json"
  "$python_bin" - "$jobs_json" "$principal_kind" <<'PY'
import json
import sys

jobs = json.load(open(sys.argv[1], encoding="utf-8"))
principal_kind = sys.argv[2]
if principal_kind == "bootstrap":
    selected = [j for j in jobs if j.get("kind") == "deployment.apply" and str(j.get("created_by", "")).startswith("bootstrap:")]
else:
    selected = [j for j in jobs if j.get("kind") == "deployment.apply" and j.get("created_by") == "local-owner"]
failed = [j for j in selected if j.get("status") == "failed" and (j.get("error") or {}).get("code") == "guarded_changes"]
succeeded = [j for j in selected if j.get("status") == "succeeded"]
assert failed, selected
assert succeeded, selected
blocked = (failed[-1].get("error") or {}).get("detail", {}).get("blocked")
assert blocked == ["modules.m1c_fixture.config.marker (data_migrate; migrate-m1c-fixture-marker)"], blocked
assert failed[-1].get("request", {}).get("allow_risky") is False, failed[-1]
assert succeeded[-1].get("request", {}).get("allow_risky") is True, succeeded[-1]
print(f"{principal_kind}_guarded_job={failed[-1]['id']}")
print(f"{principal_kind}_risky_job={succeeded[-1]['id']}")
PY
}

stage_fixture_marker() {
  directory=$1
  value=$2
  label=$3
  workspace=$directory/workspace
  fixture_root=$directory/catalog/modules
  candidate=$directory/$label-config.yml
  "$python_bin" - "$workspace/config.yml" "$candidate" "$value" <<'PY'
import json
import re
import sys

source, target, value = sys.argv[1:]
body = open(source, encoding="utf-8").read()
with open(target, "w", encoding="utf-8") as stream:
    try:
        document = json.loads(body)
    except json.JSONDecodeError:
        body, count = re.subn(r"^(\s*marker:\s*)\S+\s*$", rf"\g<1>{value}", body, count=1, flags=re.MULTILINE)
        assert count == 1, "fixture marker was not found exactly once"
        stream.write(body)
    else:
        marker = document["modules"]["m1c_fixture"]["config"]["marker"]
        assert isinstance(marker, str), marker
        document["modules"]["m1c_fixture"]["config"]["marker"] = value
        json.dump(document, stream, separators=(",", ":"), sort_keys=True)
        stream.write("\n")
PY
  "$anas_bin" config import "$candidate" -w "$workspace" --root "$fixture_root" --json \
    >"$directory/$label-change.json" || fail "stage $label guarded change through managed config import"
}

setup_run() {
  require_root
  validate_inputs
  directory=$(workdir_for_run)
  [ ! -e "$directory" ] || fail "test workdir already exists: $directory"
  mkdir -m 0700 "$directory"
  workspace=$directory/workspace
  fixture_root=$(write_fixture_module "$directory")
  initial=$directory/initial-config.yml
  {
    printf '%s\n' 'module_source: official'
    printf '%s\n' 'modules:'
    printf '%s\n' '  m1c_fixture:'
    printf '%s\n' '    config:'
    printf '%s\n' '      marker: baseline'
    printf '%s\n' 'global:'
    printf '%s\n' '  base_domain: m1c.test'
    printf '%s\n' '  email: admin@m1c.test'
    printf '%s\n' '  timezone: UTC'
    printf '%s\n' 'env:'
    printf '  CONTAINER_PREFIX: anas_m1c_%s_\n' "$run_id"
    printf '  NETWORK_PREFIX: anas_m1c_%s_\n' "$run_id"
  } >"$initial"
  "$anas_bin" init "$workspace" --config "$initial" --module-root "$fixture_root" -y --json >"$directory/init.json" 2>"$directory/init.log" || fail "initialize M1C fixture workspace"
  "$python_bin" - "$workspace/.anas/module-view.json" "$fixture_root" <<'PY'
import json, os, sys
with open(sys.argv[1], "w", encoding="utf-8") as stream:
    json.dump({"api_version":"anas.module-view/v1","digest":"m1c-browser-e2e","module_root":os.path.abspath(sys.argv[2]),"installations":{}}, stream)
    stream.write("\n")
os.chmod(sys.argv[1], 0o600)
PY
  "$anas_bin" apply -w "$workspace" --root "$fixture_root" --update-lock --no-snapshot -y --json >"$directory/baseline-apply.json" 2>"$directory/baseline-apply.log" || fail "apply baseline fixture deployment"
  stage_fixture_marker "$directory" bootstrap-change bootstrap

  tls_dir=$directory/tls
  mkdir -m 0700 "$tls_dir"
  {
    printf '%s\n' 'api_version: anas.console-config/v1'
    printf '%s\n' 'mode: lan'
    printf 'port: %s\n' "$port"
    printf 'console_store: %s\n' "$directory/console-store"
    printf '%s\n' 'workspaces:'
    printf '%s\n' '  - id: main'
    printf '    path: %s\n' "$workspace"
    printf '%s\n' 'tls:'
    printf '%s\n' '  temporary:'
    printf '    certificate: %s\n' "$tls_dir/temp-console.crt"
    printf '    private_key: %s\n' "$tls_dir/temp-console.key"
    printf '%s\n' '    ip_addresses:'
    printf '      - %s\n' "$lan_ip"
    printf '%s\n' '      - 127.0.0.1'
  } >"$directory/anasd.yml"
  chmod 0600 "$directory/anasd.yml"
  "$anas_bin" console tls --self-signed --config "$directory/anasd.yml" --ttl 30m --json >"$directory/bootstrap-token.json" || fail "generate temporary TLS and bootstrap token"
  owner_password=M1C-Owner-$($openssl_bin rand -hex 18)-Aa1
  printf '%s\n' "$owner_password" >"$directory/owner-password"
  chmod 0600 "$directory/owner-password"
  owner_password=

  start_daemon "$directory"
  wait_http "http://127.0.0.1:$port/api/v1/system" "$directory" 200
  printf '%s\n' "$fault_proxy" >"$directory/proxy.path"
  nohup "$fault_proxy" -listen "0.0.0.0:$fault_port" -upstream "http://127.0.0.1:$port" </dev/null >"$directory/proxy.log" 2>&1 &
  printf '%s\n' "$!" >"$directory/proxy.pid"
  wait_http "http://127.0.0.1:$fault_port/emergency" "$directory" 200
  "$python_bin" - "$directory/metadata.json" "$directory" "$lan_ip" "$port" "$fault_port" <<'PY'
import json, os, sys
path, directory, ip, port, fault = sys.argv[1:]
with open(path, "w", encoding="utf-8") as stream:
    json.dump({
        "workdir": directory,
        "bootstrap_origin": f"http://{ip}:{port}",
        "full_origin": f"https://{ip}:{port}",
        "fault_origin": f"http://{ip}:{fault}",
        "bootstrap_token_document": os.path.join(directory, "bootstrap-token.json"),
        "owner_password_file": os.path.join(directory, "owner-password"),
        "blocker": "modules.m1c_fixture.config.marker (data_migrate; migrate-m1c-fixture-marker)",
        "confirmation_word": "APPLY",
    }, stream, separators=(",", ":"))
    stream.write("\n")
os.chmod(path, 0o600)
PY
  printf 'metadata=%s\n' "$directory/metadata.json"
  printf 'bootstrap_origin=http://%s:%s\n' "$lan_ip" "$port"
  printf 'fault_origin=http://%s:%s\n' "$lan_ip" "$fault_port"
  printf '%s\n' 'ready=m1c_bootstrap_browser'
}

advance_full() {
  require_root
  validate_inputs
  directory=$(workdir_for_run)
  [ -d "$directory" ] || fail "test workdir is missing"
  verify_risky_jobs "$directory" bootstrap
  stop_process "$directory" proxy
  stop_process "$directory" daemon
  stage_fixture_marker "$directory" full-change full
  owner_password=$(sed -n '1p' "$directory/owner-password")
  ANAS_E2E_OWNER_PASSWORD=$owner_password "$auth_fixture" seed-full "$directory/console-store" >"$directory/seed-full.json" || fail "seed full-state local owner"
  owner_password=

  iam_name=anas_m1c_iam_$run_id
  printf '%s\n' "$iam_name" >"$directory/iam.name"
  if "$docker_bin" image inspect "$iam_image" >/dev/null 2>&1; then
    printf '%s\n' false >"$directory/iam.image-created"
  else
    "$docker_bin" pull "$iam_image" >"$directory/iam-pull.log" || fail "pull pinned Casdoor all-in-one image"
    printf '%s\n' true >"$directory/iam.image-created"
  fi
  "$docker_bin" run -d --name "$iam_name" --network bridge "$iam_image" >"$directory/iam.container-id" || fail "start real Casdoor IAM container"
  attempt=0
  while :; do
    "$docker_bin" inspect -f '{{.State.Running}}' "$iam_name" 2>/dev/null | grep -qx true || fail "Casdoor IAM exited before readiness"
    iam_ip=$($docker_bin inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$iam_name")
    status=$($curl_bin -sS -o "$directory/iam-ready" -w '%{http_code}' "http://$iam_ip:8000/" 2>/dev/null || true)
    [ "$status" != 000 ] && break
    attempt=$((attempt + 1))
    [ "$attempt" -lt 180 ] || fail "Casdoor IAM did not expose HTTP"
    sleep 1
  done
  "$docker_bin" image inspect -f '{{index .RepoDigests 0}}' "$iam_image" >"$directory/iam.image-digest" 2>/dev/null || printf '%s\n' "$iam_image" >"$directory/iam.image-digest"

  start_daemon "$directory"
  wait_http "https://127.0.0.1:$port/api/v1/system" "$directory" 200
  printf 'full_origin=https://%s:%s\n' "$lan_ip" "$port"
  printf 'iam_container=%s\n' "$iam_name"
  printf '%s\n' 'ready=m1c_full_browser_iam_running'
}

stop_iam() {
  require_root
  validate_inputs
  directory=$(workdir_for_run)
  iam_name=$(sed -n '1p' "$directory/iam.name")
  "$docker_bin" stop "$iam_name" >"$directory/iam-stop.log" || fail "stop Casdoor IAM container"
  "$docker_bin" inspect -f '{{.State.Running}}' "$iam_name" | grep -qx false || fail "Casdoor IAM remains running"
  printf '%s\n' 'iam=stopped'
  printf '%s\n' 'ready=m1c_full_browser_iam_stopped'
}

record_browser_check() {
  require_root
  validate_inputs
  check=${2:-}
  case "$check" in
    emergency-ui|bootstrap-session-refresh|task-sse|bootstrap-risky-ui|full-risky-ui|iam-running-login|iam-stopped-login) ;;
    *) fail "unknown browser check marker" ;;
  esac
  directory=$(workdir_for_run)
  [ -d "$directory" ] || fail "test workdir is missing"
  printf 'observed_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$directory/browser-$check"
  printf 'browser_check=%s\n' "$check"
}

verify_run() {
  require_root
  validate_inputs
  directory=$(workdir_for_run)
  for check in emergency-ui bootstrap-session-refresh task-sse bootstrap-risky-ui full-risky-ui iam-running-login iam-stopped-login; do
    [ -f "$directory/browser-$check" ] || fail "browser check is missing: $check"
  done
  verify_risky_jobs "$directory" full
  iam_name=$(sed -n '1p' "$directory/iam.name")
  "$docker_bin" inspect -f '{{.State.Running}}' "$iam_name" | grep -qx false || fail "IAM must be stopped for the R-104 proof"
  pid=$(sed -n '1p' "$directory/daemon.pid")
  expected=$(sed -n '1p' "$directory/daemon.path")
  pid_matches "$pid" "$expected" || fail "anasd is not running after IAM-stop local login"
  printf '%s\n' 'PASS: CONSOLE-R-103 local login succeeded with real IAM running'
  printf '%s\n' 'PASS: CONSOLE-R-104 local login succeeded after real IAM stopped'
  printf '%s\n' 'PASS: CONSOLE-R-113 emergency UI rendered while main JavaScript returned 503'
  printf '%s\n' 'PASS: CONSOLE-R-131 bootstrap and full risky UI flows used exact blocker, confirmation word, confirmation and local step-up'
  printf 'iam_image=%s\n' "$(sed -n '1p' "$directory/iam.image-digest")"
  printf '%s\n' 'daemon=running'
}

cleanup_run() {
  require_root
  validate_inputs
  directory=$(workdir_for_run)
  case "$directory" in /tmp/anas-m1c.*) ;; *) fail "refuse cleanup outside /tmp/anas-m1c.*" ;; esac
  [ -d "$directory" ] || { printf '%s\n' 'cleanup=already-absent'; return; }
  stop_process "$directory" proxy
  stop_process "$directory" daemon
  if [ -f "$directory/iam.name" ]; then
    iam_name=$(sed -n '1p' "$directory/iam.name")
    "$docker_bin" rm -f "$iam_name" >/dev/null 2>&1 || true
  fi
  "$anas_bin" stop -w "$directory/workspace" >/dev/null 2>&1 || true
  if [ -f "$directory/iam.image-created" ] && [ "$(sed -n '1p' "$directory/iam.image-created")" = true ]; then
    "$docker_bin" image rm "$iam_image" >/dev/null 2>&1 || true
  fi
  rm -rf -- "$directory"
  printf '%s\n' 'cleanup=complete'
}

case "$command_name" in
  setup) setup_run ;;
  advance-full) advance_full ;;
  stop-iam) stop_iam ;;
  record) record_browser_check "$@" ;;
  verify) verify_run ;;
  cleanup) cleanup_run ;;
  *) printf 'usage: %s {setup|advance-full|stop-iam|record CHECK|verify|cleanup}\n' "$0" >&2; exit 2 ;;
esac
