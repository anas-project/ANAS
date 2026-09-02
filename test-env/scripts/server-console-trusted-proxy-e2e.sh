#!/usr/bin/env sh
# REQUIREMENTS: CONSOLE-R-072 CONSOLE-R-101 CONSOLE-R-114 CONSOLE-R-122
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
umask 077

run_id=${ANAS_E2E_RUN_ID:-}
direct_port=${ANAS_E2E_DIRECT_PORT:-}
proxy_port=${ANAS_E2E_PROXY_PORT:-}
anas_bin=${ANAS_BIN:-}
anasd_bin=${ANASD_BIN:-}
auth_fixture=${ANAS_AUTH_FIXTURE_BIN:-}
module_root=${ANAS_MODULE_ROOT:-}
curl_bin=${CURL:-$(command -v curl || true)}
openssl_bin=${OPENSSL:-$(command -v openssl || true)}
python_bin=${PYTHON:-$(command -v python3 || true)}

fail() {
  printf 'trusted-proxy E2E failed: %s\n' "$1" >&2
  exit 1
}

case "$run_id" in
  ''|*[!A-Za-z0-9._-]*) fail "ANAS_E2E_RUN_ID must use only letters, digits, dot, underscore, or hyphen" ;;
esac
for value in "$direct_port" "$proxy_port"; do
  case "$value" in ''|*[!0-9]*) fail "E2E ports must be numeric" ;; esac
  [ "$value" -ge 7700 ] && [ "$value" -le 7799 ] || fail "E2E ports must be between 7700 and 7799"
done
[ "$direct_port" != "$proxy_port" ] || fail "direct and trusted-proxy ports must differ"
[ "$(id -u)" -eq 0 ] || fail "run as root so production file policies are exercised"
for item in "$anas_bin" "$anasd_bin" "$auth_fixture"; do
  [ -n "$item" ] && [ -f "$item" ] && [ ! -L "$item" ] && [ -x "$item" ] ||
    fail "ANAS_BIN, ANASD_BIN, and ANAS_AUTH_FIXTURE_BIN must be executable regular files"
done
[ -n "$module_root" ] && [ -d "$module_root" ] && [ ! -L "$module_root" ] || fail "ANAS_MODULE_ROOT must be an unpacked directory"
[ -d "$(dirname -- "$module_root")/contracts" ] || fail "contracts must be beside ANAS_MODULE_ROOT"
for item in "$curl_bin" "$openssl_bin" "$python_bin"; do
  [ -n "$item" ] || fail "curl, openssl, and python3 are required"
done

directory=/tmp/anas-trusted-proxy-e2e.$run_id
case "$directory" in /tmp/anas-trusted-proxy-e2e.*) ;; *) fail "unsafe work directory" ;; esac
[ ! -e "$directory" ] || fail "test work directory already exists: $directory"
mkdir -m 0700 "$directory"

pid_matches() {
  pid=$1
  [ -n "$pid" ] && [ -d "/proc/$pid" ] || return 1
  [ "$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)" = "$(readlink -f "$anasd_bin")" ]
}

cleanup() {
  status=$?
  set +e
  if [ -f "$directory/daemon.pid" ]; then
    pid=$(sed -n '1p' "$directory/daemon.pid")
    if pid_matches "$pid"; then
      kill "$pid" 2>/dev/null
      count=0
      while pid_matches "$pid" && [ "$count" -lt 100 ]; do
        sleep 0.1
        count=$((count + 1))
      done
      pid_matches "$pid" && kill -KILL "$pid" 2>/dev/null
    fi
  fi
  [ -d "$directory/workspace" ] && "$anas_bin" stop -w "$directory/workspace" >/dev/null 2>&1
  if [ "$status" -ne 0 ] && [ -f "$directory/anasd.log" ]; then
    printf '%s\n' '--- anasd log tail ---' >&2
    tail -n 80 "$directory/anasd.log" >&2
  fi
  rm -rf -- "$directory"
  trap - EXIT INT TERM
  exit "$status"
}
trap cleanup EXIT INT TERM

json_value() {
  "$python_bin" - "$1" "$2" <<'PY'
import json
import sys

value = json.load(open(sys.argv[1], encoding="utf-8"))
for component in sys.argv[2].split("."):
    value = value[component]
if isinstance(value, bool):
    print("true" if value else "false")
elif value is None:
    print("null")
else:
    print(value)
PY
}

expect_problem() {
  file=$1
  expected=$2
  actual=$(json_value "$file" code)
  [ "$actual" = "$expected" ] || fail "problem code is $actual, want $expected"
}

wait_http() {
  url=$1
  attempt=0
  while :; do
    status=$($curl_bin --noproxy '*' -ksS -o "$directory/ready.json" -w '%{http_code}' "$url" 2>/dev/null || true)
    [ "$status" = 200 ] && return 0
    attempt=$((attempt + 1))
    [ "$attempt" -lt 200 ] || fail "timed out waiting for $url (last HTTP $status)"
    sleep 0.1
  done
}

fixture_root=$directory/catalog/modules
fixture_module=$fixture_root/proxy_fixture
mkdir -p "$fixture_module"
cp -a "$(dirname -- "$module_root")/contracts" "$directory/catalog/contracts"
cat >"$fixture_module/module.yml" <<'YAML'
api_version: anas.module/v1
kind: Module
name: proxy_fixture
version: 1.0.0
revision: 1
app_version: 1.0.0
abi:
  supports: [anas.module-hook/v1]
title: Trusted proxy E2E fixture
description: Isolated guarded-change fixture.
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
      apply: migrate-proxy-fixture-marker
      description: Trusted-proxy E2E guarded marker.
YAML
cat >"$fixture_module/docker-compose.yml" <<'YAML'
services:
  anas_proxy_fixture:
    image: docker.io/library/busybox:1.37.0
    restart: unless-stopped
    command: ["sh", "-c", "trap 'exit 0' TERM INT; while :; do sleep 3600; done"]
YAML

workspace=$directory/workspace
cat >"$directory/initial.yml" <<YAML
module_source: official
modules:
  proxy_fixture:
    config:
      marker: baseline
global:
  base_domain: proxy-e2e.test
  email: admin@proxy-e2e.test
env:
  CONTAINER_PREFIX: anas_proxy_${run_id}_
  NETWORK_PREFIX: anas_proxy_${run_id}_
YAML
"$anas_bin" init "$workspace" --config "$directory/initial.yml" --module-root "$fixture_root" -y --json >"$directory/init.json" 2>"$directory/init.log" || fail "initialize fixture workspace"
"$python_bin" - "$workspace/.anas/module-view.json" "$fixture_root" <<'PY'
import json
import os
import sys

with open(sys.argv[1], "w", encoding="utf-8") as stream:
    json.dump({"api_version":"anas.module-view/v1","digest":"trusted-proxy-e2e","module_root":os.path.abspath(sys.argv[2]),"installations":{}}, stream)
    stream.write("\n")
os.chmod(sys.argv[1], 0o600)
PY
"$anas_bin" apply -w "$workspace" --root "$fixture_root" --update-lock --no-snapshot -y --json >"$directory/baseline-apply.json" 2>"$directory/baseline-apply.log" || fail "apply baseline fixture"

"$python_bin" - "$workspace/config.yml" "$directory/guarded.yml" <<'PY'
import json
import re
import sys

source, target = sys.argv[1:]
body = open(source, encoding="utf-8").read()
try:
    document = json.loads(body)
except json.JSONDecodeError:
    body, count = re.subn(r"^(\s*marker:\s*)\S+\s*$", r"\g<1>guarded", body, count=1, flags=re.MULTILINE)
    assert count == 1
    open(target, "w", encoding="utf-8").write(body)
else:
    document["modules"]["proxy_fixture"]["config"]["marker"] = "guarded"
    with open(target, "w", encoding="utf-8") as stream:
        json.dump(document, stream, separators=(",", ":"), sort_keys=True)
        stream.write("\n")
PY
"$anas_bin" config import "$directory/guarded.yml" -w "$workspace" --root "$fixture_root" --json >"$directory/guarded-import.json" || fail "stage guarded desired configuration"

identity_dir=$directory/identity
tls_dir=$directory/tls
mkdir -m 0700 "$identity_dir" "$tls_dir"
"$openssl_bin" req -new -x509 -newkey rsa:2048 -nodes -days 2 -subj '/CN=ANAS trusted proxy test CA' -keyout "$identity_dir/ca.key" -out "$identity_dir/ca.crt" >/dev/null 2>&1
cat >"$identity_dir/client.ext" <<'EOF'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=clientAuth
EOF
for name in client wrong; do
  "$openssl_bin" req -new -newkey rsa:2048 -nodes -subj "/CN=ANAS trusted proxy $name" -keyout "$identity_dir/$name.key" -out "$identity_dir/$name.csr" >/dev/null 2>&1
  if [ "$name" = client ]; then
    "$openssl_bin" x509 -req -days 2 -in "$identity_dir/$name.csr" -CA "$identity_dir/ca.crt" -CAkey "$identity_dir/ca.key" -CAcreateserial -extfile "$identity_dir/client.ext" -out "$identity_dir/$name.crt" >/dev/null 2>&1
  else
    "$openssl_bin" x509 -req -days 2 -in "$identity_dir/$name.csr" -CA "$identity_dir/ca.crt" -CAkey "$identity_dir/ca.key" -CAserial "$identity_dir/ca.srl" -extfile "$identity_dir/client.ext" -out "$identity_dir/$name.crt" >/dev/null 2>&1
  fi
done
client_spki=$("$openssl_bin" x509 -in "$identity_dir/client.crt" -pubkey -noout | "$openssl_bin" pkey -pubin -outform DER | "$openssl_bin" dgst -sha256 -r | awk '{print $1}')
case "$client_spki" in
  *[!0-9a-f]*|'') fail "could not derive lowercase client SPKI digest" ;;
esac
chmod 0600 "$identity_dir"/*

cat >"$directory/anasd.yml" <<YAML
api_version: anas.console-config/v1
mode: loopback
port: $direct_port
allowed_dns_hosts:
  - direct.proxy.test
console_store: $directory/console-store
workspaces:
  - id: main
    path: $workspace
tls:
  temporary:
    certificate: $tls_dir/temp-console.crt
    private_key: $tls_dir/temp-console.key
    dns_names:
      - direct.proxy.test
      - proxy.proxy.test
    ip_addresses:
      - 127.0.0.1
trusted_proxy:
  bind_address: 127.0.0.1
  port: $proxy_port
  public_url: https://proxy.proxy.test:$proxy_port
  allowed_source_ips:
    - 127.0.0.1
  allowed_dns_hosts:
    - proxy.proxy.test
  oidc_issuer: https://iam.proxy.test
  platform_admin_group: Admins
  client_ca: $identity_dir/ca.crt
  client_spki_sha256:
    - $client_spki
YAML
chmod 0600 "$directory/anasd.yml"
"$anas_bin" console tls --self-signed --config "$directory/anasd.yml" --ttl 30m --json >"$directory/bootstrap.json" || fail "generate temporary serving certificate"
owner_password=Proxy-E2E-$($openssl_bin rand -hex 18)-Aa1
ANAS_E2E_OWNER_PASSWORD=$owner_password "$auth_fixture" seed-full "$directory/console-store" >"$directory/seed-full.json" || fail "seed full-state owner"
owner_password=

nohup "$anasd_bin" --config "$directory/anasd.yml" </dev/null >"$directory/anasd.log" 2>&1 &
printf '%s\n' "$!" >"$directory/daemon.pid"
wait_http "https://127.0.0.1:$direct_port/api/v1/system"

direct_url=https://direct.proxy.test:$direct_port
proxy_url=https://proxy.proxy.test:$proxy_port
server_ca=$tls_dir/temp-console.crt

status=$($curl_bin --noproxy '*' -sS --max-time 5 --cacert "$server_ca" --resolve "proxy.proxy.test:$proxy_port:127.0.0.1" -o "$directory/no-client" -w '%{http_code}' "$proxy_url/api/v1/system" 2>/dev/null || true)
[ "$status" != 200 ] || fail "trusted listener accepted a request without a client certificate"
status=$($curl_bin --noproxy '*' -sS --max-time 5 --cacert "$server_ca" --resolve "proxy.proxy.test:$proxy_port:127.0.0.1" --cert "$identity_dir/wrong.crt" --key "$identity_dir/wrong.key" -o "$directory/wrong-client" -w '%{http_code}' "$proxy_url/api/v1/system" 2>/dev/null || true)
[ "$status" != 200 ] || fail "trusted listener accepted an unpinned client certificate"
status=$($curl_bin --noproxy '*' -sS --max-time 5 -o "$directory/plaintext-proxy" -w '%{http_code}' "http://127.0.0.1:$proxy_port/api/v1/system" 2>/dev/null || true)
[ "$status" != 200 ] || fail "trusted listener accepted plaintext HTTP"

issuer=https://iam.proxy.test
subject=platform-admin-e2e
expires_at=$(($(date +%s) + 3600))
cookie_jar=$directory/proxy.cookies

proxy_request() {
  assertion=$1
  auth_time=$2
  shift 2
  "$curl_bin" --noproxy '*' -sS --max-time 20 --cacert "$server_ca" \
    --resolve "proxy.proxy.test:$proxy_port:127.0.0.1" \
    --cert "$identity_dir/client.crt" --key "$identity_dir/client.key" \
    -H "X-Anas-Identity-Issuer: $issuer" \
    -H "X-Anas-Identity-Subject: $subject" \
    -H 'X-Anas-Identity-Role: platform_admin' \
    -H 'X-Anas-Identity-Group: Admins' \
    -H "X-Anas-Identity-Auth-Time: $auth_time" \
    -H "X-Anas-Identity-Expires-At: $expires_at" \
    -H "X-Anas-Identity-Assertion: $assertion" \
    "$@"
}

now=$(date +%s)
status=$(proxy_request session-assertion "$now" -c "$cookie_jar" -b "$cookie_jar" -o "$directory/system.json" -w '%{http_code}' "$proxy_url/api/v1/system")
[ "$status" = 200 ] || fail "pinned mTLS client could not reach trusted listener (HTTP $status)"
"$python_bin" - "$directory/system.json" "$proxy_url" <<'PY'
import json
import sys

document = json.load(open(sys.argv[1], encoding="utf-8"))
assert document["listener"] == "trusted_proxy", document
assert document["proxy_url"] == sys.argv[2], document
assert document["direct_recovery_urls"], document
PY

status=$(proxy_request session-assertion "$now" -c "$cookie_jar" -b "$cookie_jar" -o "$directory/session.json" -w '%{http_code}' "$proxy_url/api/v1/auth/session")
[ "$status" = 200 ] || fail "create proxy session returned HTTP $status"
csrf=$(json_value "$directory/session.json" csrf_token)

status=$(proxy_request session-assertion "$now" -c "$cookie_jar" -b "$cookie_jar" -H "Origin: $proxy_url" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -d '{}' -o "$directory/proxy-login.json" -w '%{http_code}' "$proxy_url/api/v1/auth/login")
[ "$status" = 404 ] || fail "trusted-proxy local login returned HTTP $status instead of 404"

stale_time=$((now - 301))
status=$(proxy_request stale-assertion "$stale_time" -c "$cookie_jar" -b "$cookie_jar" -H "Origin: $proxy_url" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -d '{"action":"deployment.apply","workspace_id":"main"}' -o "$directory/stale-step-up.json" -w '%{http_code}' "$proxy_url/api/v1/auth/step-up")
[ "$status" = 428 ] || fail "stale OIDC authentication returned HTTP $status instead of 428"
expect_problem "$directory/stale-step-up.json" recent_auth_required

issue_step_up() {
  assertion=$1
  output=$2
  current=$(date +%s)
  status=$(proxy_request "$assertion" "$current" -c "$cookie_jar" -b "$cookie_jar" -H "Origin: $proxy_url" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -d '{"action":"deployment.apply","workspace_id":"main"}' -o "$output" -w '%{http_code}' "$proxy_url/api/v1/auth/step-up")
  [ "$status" = 200 ] || fail "fresh proxy step-up returned HTTP $status"
}

plan_with_proof() {
  assertion=$1
  proof=$2
  output=$3
  current=$(date +%s)
  body=$(printf '{"step_up_proof":"%s"}' "$proof")
  proxy_request "$assertion" "$current" -c "$cookie_jar" -b "$cookie_jar" -H "Origin: $proxy_url" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -d "$body" -o "$output" -w '%{http_code}' "$proxy_url/api/v1/workspaces/main/plans"
}

apply_plan() {
  assertion=$1
  plan_file=$2
  allow_risky=$3
  idempotency=$4
  output=$5
  proof=$6
  "$python_bin" - "$plan_file" "$allow_risky" "$proof" "$directory/apply-body.json" <<'PY'
import json
import sys

plan = json.load(open(sys.argv[1], encoding="utf-8"))
body = {
    "plan_job_id": plan["confirmation"]["plan_job_id"],
    "confirmation_token": plan["confirmation"]["token"],
    "step_up_proof": sys.argv[3],
    "expected_config_validator": plan["plan"]["config_validator"],
    "expected_plan_digest": plan["plan"]["digest"],
    "allow_risky": sys.argv[2] == "true",
    "no_snapshot": True,
}
json.dump(body, open(sys.argv[4], "w", encoding="utf-8"), separators=(",", ":"))
PY
  current=$(date +%s)
  proxy_request "$assertion" "$current" -c "$cookie_jar" -b "$cookie_jar" -H "Origin: $proxy_url" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -H "Idempotency-Key: $idempotency" --data-binary "@$directory/apply-body.json" -o "$output" -w '%{http_code}' "$proxy_url/api/v1/workspaces/main/actions/apply"
}

issue_step_up assertion-a "$directory/step-a.json"
proof_a=$(json_value "$directory/step-a.json" proof)
status=$(plan_with_proof assertion-b "$proof_a" "$directory/assertion-mismatch.json")
[ "$status" = 409 ] || fail "step-up proof was not bound to its trusted assertion (HTTP $status)"
expect_problem "$directory/assertion-mismatch.json" step_up_invalid

issue_step_up assertion-b "$directory/step-b.json"
proof_b=$(json_value "$directory/step-b.json" proof)
status=$(plan_with_proof assertion-b "$proof_b" "$directory/plan-normal.json")
[ "$status" = 200 ] || fail "proxy plan returned HTTP $status"
status=$(apply_plan assertion-b "$directory/plan-normal.json" false "proxy-normal-$run_id" "$directory/apply-normal.json" "$proof_b")
[ "$status" = 202 ] || fail "ordinary proxy apply returned HTTP $status"
normal_job=$(json_value "$directory/apply-normal.json" job.id)

wait_job() {
  assertion=$1
  job_id=$2
  expected=$3
  output=$4
  attempt=0
  while :; do
    current=$(date +%s)
    status=$(proxy_request "$assertion" "$current" -c "$cookie_jar" -b "$cookie_jar" -o "$output" -w '%{http_code}' "$proxy_url/api/v1/jobs/$job_id")
    [ "$status" = 200 ] || fail "job $job_id returned HTTP $status"
    state=$(json_value "$output" job.status)
    [ "$state" = "$expected" ] && return 0
    case "$state" in failed|succeeded|canceled|interrupted) fail "job $job_id reached $state, want $expected" ;; esac
    attempt=$((attempt + 1))
    [ "$attempt" -lt 300 ] || fail "timed out waiting for job $job_id"
    sleep 0.1
  done
}
wait_job assertion-b "$normal_job" failed "$directory/normal-job.json"
"$python_bin" - "$directory/normal-job.json" <<'PY'
import json
import sys

job = json.load(open(sys.argv[1], encoding="utf-8"))["job"]
assert job["error"]["code"] == "guarded_changes", job
assert job["error"]["detail"]["blocked"] == ["modules.proxy_fixture.config.marker (data_migrate; migrate-proxy-fixture-marker)"], job
PY

status=$(plan_with_proof assertion-b "$proof_b" "$directory/reuse-plan.json")
[ "$status" = 200 ] || fail "could not create proof-reuse plan"
status=$(apply_plan assertion-b "$directory/reuse-plan.json" true "proxy-reuse-$run_id" "$directory/reuse-apply.json" "$proof_b")
[ "$status" = 409 ] || fail "consumed proxy step-up proof returned HTTP $status instead of 409"
expect_problem "$directory/reuse-apply.json" step_up_consumed

issue_step_up assertion-c "$directory/step-c.json"
proof_c=$(json_value "$directory/step-c.json" proof)
status=$(plan_with_proof assertion-c "$proof_c" "$directory/plan-risky.json")
[ "$status" = 200 ] || fail "fresh risky proxy plan returned HTTP $status"
status=$(apply_plan assertion-c "$directory/plan-risky.json" true "proxy-risky-$run_id" "$directory/apply-risky.json" "$proof_c")
[ "$status" = 202 ] || fail "allow_risky proxy apply returned HTTP $status"
risky_job=$(json_value "$directory/apply-risky.json" job.id)
wait_job assertion-c "$risky_job" succeeded "$directory/risky-job.json"

grep -q '"semantic_role":"platform_admin"' "$directory/console-store/audit.jsonl" || fail "audit omits semantic platform_admin role"
grep -q '"directory_group":"Admins"' "$directory/console-store/audit.jsonl" || fail "audit omits resolved directory group"
for secret in session-assertion stale-assertion assertion-a assertion-b assertion-c; do
  if grep -R -F "$secret" "$directory/console-store" "$directory/anasd.log" >/dev/null 2>&1; then
    fail "raw proxy assertion was persisted or logged"
  fi
done

status=$($curl_bin --noproxy '*' -sS --max-time 10 --cacert "$server_ca" --resolve "direct.proxy.test:$direct_port:127.0.0.1" -o "$directory/direct-system.json" -w '%{http_code}' "$direct_url/api/v1/system")
[ "$status" = 200 ] || fail "direct recovery listener stopped serving after proxy flow"

printf '%s\n' 'PASS: CONSOLE-R-072 trusted listener required TLS, a CA-valid pinned client certificate, and an exact source IP'
printf '%s\n' 'PASS: CONSOLE-R-101 local-login route returned 404 on the trusted-proxy listener while direct recovery remained available'
printf '%s\n' 'PASS: CONSOLE-R-114 stale OIDC auth returned 428; fresh proof was assertion-bound and rejected after one apply consumption'
printf '%s\n' 'PASS: CONSOLE-R-122 ordinary apply preserved every guarded blocker and fresh source-aware step-up plus server confirmation allowed risky apply'
printf 'normal_job=%s\n' "$normal_job"
printf 'risky_job=%s\n' "$risky_job"
printf 'client_spki_sha256=%s\n' "$client_spki"
