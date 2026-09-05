#!/usr/bin/env sh
# REQUIREMENTS: CONSOLE-R-181 CONSOLE-R-147
#
# Module Command invocation is the one write surface whose safety rests on
# bindings that only exist end to end: the digest of a descriptor frozen by the
# active deployment, a single-use step-up bound to that digest, an idempotency
# key, and a durable job that actually runs a real executor. A unit test can
# assert each of those against a fake; only a real daemon over real TLS with a
# real workspace proves they hold together.
#
# What this proves that nothing else does:
#
#   R-181  A normal command runs from an owner session with the digest alone; a
#          stale digest is refused with 412 after the descriptor changes under a
#          client that already read it; a destructive command is refused without
#          a step-up; a proof spends exactly once; and an idempotent retry runs
#          the executor once, not twice.
#   R-147  The executor's environment is built by the service. The daemon is
#          started with a marker variable in its own environment, and the
#          executor records everything it received -- the marker must not be
#          there.
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
umask 077

command_name=${1:-}
run_id=${ANAS_E2E_RUN_ID:-}
port=${ANAS_E2E_PORT:-7791}
anas_bin=${ANAS_BIN:-}
anasd_bin=${ANASD_BIN:-}
auth_fixture=${ANAS_AUTH_FIXTURE:-}
command_fixture=${ANAS_COMMAND_FIXTURE:-}
python_bin=${PYTHON:-$(command -v python3 || true)}
curl_bin=${CURL:-$(command -v curl || true)}
openssl_bin=${OPENSSL:-$(command -v openssl || true)}

# A value that can only reach a subprocess by inheriting the daemon's
# environment. Nothing in the workspace or the service configuration mentions it.
leak_probe_name=ANAS_E2E_DAEMON_LEAK_PROBE
leak_probe_value=daemon-environment-must-not-be-inherited

fail() {
  printf 'console command invoke E2E failed: %s\n' "$1" >&2
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
  for item in "$anas_bin" "$anasd_bin" "$auth_fixture" "$command_fixture"; do
    [ -n "$item" ] && [ -f "$item" ] && [ ! -L "$item" ] && [ -x "$item" ] ||
      fail "ANAS_BIN, ANASD_BIN, ANAS_AUTH_FIXTURE and ANAS_COMMAND_FIXTURE must name executable regular, non-symlink files"
  done
  for item in "$python_bin" "$curl_bin" "$openssl_bin"; do
    [ -n "$item" ] || fail "python3, curl and openssl are required"
  done
}

workdir_for_run() {
  printf '/tmp/anas-command-invoke.%s\n' "$run_id"
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

origin_for_run() {
  printf 'https://127.0.0.1:%s\n' "$port"
}

# Sign in and keep the cookie jar plus the CSRF token every write needs.
sign_in() {
  directory=$1
  origin=$(origin_for_run)
  password=$(sed -n '1p' "$directory/owner-password")
  csrf=$($curl_bin -ksS -c "$directory/cookies" -b "$directory/cookies" \
    "$origin/api/v1/auth/csrf" | "$python_bin" -c 'import json,sys; print(json.load(sys.stdin)["csrf_token"])') ||
    fail "obtain a pre-authentication CSRF token"
  status=$("$python_bin" -c 'import json,sys; print(json.dumps({"password": sys.argv[1]}))' "$password" |
    $curl_bin -ksS -o "$directory/login.json" -w '%{http_code}' \
      -c "$directory/cookies" -b "$directory/cookies" \
      -H 'Content-Type: application/json' -H "Origin: $origin" -H "X-CSRF-Token: $csrf" \
      --data-binary @- "$origin/api/v1/auth/login")
  password=
  [ "$status" = "200" ] || fail "local owner login returned HTTP $status"
  "$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["csrf_token"])' "$directory/login.json" \
    >"$directory/csrf" || fail "read the authenticated CSRF token"
  chmod 0600 "$directory/csrf" "$directory/cookies"
}

api_get() {
  directory=$1
  path=$2
  target=$3
  $curl_bin -ksS -o "$target" -w '%{http_code}' \
    -c "$directory/cookies" -b "$directory/cookies" "$(origin_for_run)$path"
}

# Every mutating call carries Origin, CSRF and an idempotency key, exactly as the
# console does. The key is a parameter so the retry case can reuse one.
api_post() {
  directory=$1
  path=$2
  body=$3
  target=$4
  idempotency=$5
  origin=$(origin_for_run)
  csrf=$(sed -n '1p' "$directory/csrf")
  set -- -ksS -o "$target" -w '%{http_code}' \
    -c "$directory/cookies" -b "$directory/cookies" \
    -H 'Content-Type: application/json' -H "Origin: $origin" -H "X-CSRF-Token: $csrf"
  if [ -n "$idempotency" ]; then
    set -- "$@" -H "Idempotency-Key: $idempotency"
  fi
  printf '%s' "$body" | $curl_bin "$@" --data-binary @- "$origin$path"
}

invoke_path() {
  printf '/api/v1/workspaces/main/modules/demo/commands/%s/actions/invoke\n' "$1"
}

expect_status() {
  actual=$1
  expected=$2
  label=$3
  body=$4
  [ "$actual" = "$expected" ] && return 0
  printf 'response body: %s\n' "$(cat "$body" 2>/dev/null || true)" >&2
  fail "$label returned HTTP $actual, want $expected"
}

expect_problem_code() {
  body=$1
  expected=$2
  label=$3
  actual=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1])).get("code",""))' "$body" 2>/dev/null || true)
  [ "$actual" = "$expected" ] && return 0
  fail "$label returned problem code '$actual', want '$expected'"
}

job_id_from() {
  "$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["job"]["id"])' "$1"
}

# Poll the job API rather than sleeping, and fail loudly on a terminal status
# that is not success.
await_job() {
  directory=$1
  job=$2
  attempt=0
  while :; do
    status=$(api_get "$directory" "/api/v1/jobs/$job" "$directory/job.json")
    [ "$status" = "200" ] || fail "job read returned HTTP $status"
    state=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["job"]["status"])' "$directory/job.json")
    case "$state" in
      succeeded) return 0 ;;
      failed|canceled|interrupted)
        printf 'job detail: %s\n' "$(cat "$directory/job.json")" >&2
        fail "job $job reached terminal status $state"
        ;;
    esac
    attempt=$((attempt + 1))
    [ "$attempt" -lt 300 ] || fail "timed out waiting for job $job (last status $state)"
    sleep 0.1
  done
}

module_root_for() {
  printf '%s/workspace/.anas/deployments/dep-command-invoke-e2e/modules/demo\n' "$1"
}

invocation_count() {
  log=$(module_root_for "$1")/invocations.log
  [ -f "$log" ] || { printf '0\n'; return 0; }
  grep -c "^$2$" "$log" 2>/dev/null || printf '0\n'
}

setup_run() {
  require_root
  validate_inputs
  directory=$(workdir_for_run)
  [ ! -e "$directory" ] || fail "test workdir already exists: $directory"
  mkdir -m 0700 "$directory"

  workspace=$directory/workspace
  mkdir -m 0700 "$workspace"
  "$command_fixture" materialize "$workspace" >"$directory/descriptors.json" ||
    fail "materialize the Module command workspace"
  chmod 0600 "$directory/descriptors.json"

  tls_dir=$directory/tls
  mkdir -m 0700 "$tls_dir"
  {
    printf '%s\n' 'api_version: anas.console-config/v1'
    printf '%s\n' 'mode: loopback'
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
    printf '%s\n' '      - 127.0.0.1'
  } >"$directory/anasd.yml"
  chmod 0600 "$directory/anasd.yml"
  "$anas_bin" console tls --self-signed --config "$directory/anasd.yml" --ttl 30m --json \
    >"$directory/tls.json" || fail "generate the temporary TLS material"

  owner_password=Command-Owner-$($openssl_bin rand -hex 18)-Aa1
  printf '%s\n' "$owner_password" >"$directory/owner-password"
  chmod 0600 "$directory/owner-password"
  ANAS_E2E_OWNER_PASSWORD=$owner_password "$auth_fixture" seed-full "$directory/console-store" \
    >"$directory/seed-full.json" || fail "seed a full-state console with a local owner"
  owner_password=
  chmod 0600 "$directory/seed-full.json"

  printf '%s\n' "$anasd_bin" >"$directory/daemon.path"
  # The probe rides in the daemon's own environment; R-147 is the claim that it
  # stops there.
  env "$leak_probe_name=$leak_probe_value" \
    nohup "$anasd_bin" --config "$directory/anasd.yml" </dev/null >"$directory/anasd.log" 2>&1 &
  printf '%s\n' "$!" >"$directory/daemon.pid"
  wait_http "$(origin_for_run)/api/v1/system" "$directory" 200

  printf 'workdir=%s\n' "$directory"
  printf 'console_origin=%s\n' "$(origin_for_run)"
  printf '%s\n' 'ready=command_invoke'
  printf '%s\n' 'next: run: verify'
}

verify_run() {
  require_root
  validate_inputs
  require_workdir
  directory=$(workdir_for_run)
  require_daemon "$directory"
  sign_in "$directory"

  normal_digest=$("$python_bin" -c \
    'import json,sys; print(json.load(open(sys.argv[1]))["commands"]["reindex"]["digest"])' "$directory/descriptors.json")
  purge_digest=$("$python_bin" -c \
    'import json,sys; print(json.load(open(sys.argv[1]))["commands"]["purge"]["digest"])' "$directory/descriptors.json")

  # The digest the daemon serves must be the digest the fixture froze; if these
  # ever diverge every later assertion is meaningless.
  status=$(api_get "$directory" "/api/v1/workspaces/main/modules/demo/commands/reindex" "$directory/reindex.json")
  expect_status "$status" 200 "command detail" "$directory/reindex.json"
  served=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["command"]["digest"])' "$directory/reindex.json")
  [ "$served" = "$normal_digest" ] || fail "served digest $served does not match the frozen digest $normal_digest"
  if grep -q "$directory/workspace" "$directory/reindex.json"; then
    fail "the command detail response exposes the workspace path"
  fi

  # 1. Missing idempotency key is refused before anything runs.
  status=$(api_post "$directory" "$(invoke_path reindex)" \
    "{\"command_digest\":\"$normal_digest\"}" "$directory/no-key.json" "")
  expect_status "$status" 400 "invoke without Idempotency-Key" "$directory/no-key.json"
  expect_problem_code "$directory/no-key.json" idempotency_key_required "invoke without Idempotency-Key"

  # 2. Missing digest is refused.
  status=$(api_post "$directory" "$(invoke_path reindex)" '{}' "$directory/no-digest.json" "key-no-digest")
  expect_status "$status" 428 "invoke without command_digest" "$directory/no-digest.json"
  expect_problem_code "$directory/no-digest.json" module_command_digest_required "invoke without command_digest"

  # 3. A destructive command is refused without a step-up proof.
  status=$(api_post "$directory" "$(invoke_path purge)" \
    "{\"command_digest\":\"$purge_digest\"}" "$directory/purge-no-proof.json" "key-purge-no-proof")
  expect_status "$status" 428 "destructive invoke without a proof" "$directory/purge-no-proof.json"
  expect_problem_code "$directory/purge-no-proof.json" step_up_required "destructive invoke without a proof"

  # 4. A normal command refuses a proof it does not need, so a single-use
  #    credential is never spent on an operation that never required one.
  status=$(api_post "$directory" "$(invoke_path reindex)" \
    "{\"command_digest\":\"$normal_digest\",\"step_up_proof\":\"sup_$(printf 'A%.0s' $(seq 43))\"}" \
    "$directory/reindex-extra-proof.json" "key-reindex-extra-proof")
  expect_status "$status" 400 "normal invoke carrying a proof" "$directory/reindex-extra-proof.json"
  expect_problem_code "$directory/reindex-extra-proof.json" step_up_request_invalid "normal invoke carrying a proof"

  [ "$(invocation_count "$directory" reindex)" = "0" ] && [ "$(invocation_count "$directory" purge)" = "0" ] ||
    fail "a rejected invocation reached the executor"

  # 5. The happy path: a normal command runs from the owner session alone.
  status=$(api_post "$directory" "$(invoke_path reindex)" \
    "{\"command_digest\":\"$normal_digest\",\"parameters\":{\"full\":true}}" \
    "$directory/reindex-accepted.json" "key-reindex-1")
  expect_status "$status" 202 "normal invoke" "$directory/reindex-accepted.json"
  reindex_job=$(job_id_from "$directory/reindex-accepted.json")
  await_job "$directory" "$reindex_job"
  [ "$(invocation_count "$directory" reindex)" = "1" ] || fail "the reindex executor did not run exactly once"

  # 6. R-147: the executor's environment is the service's construction, not the
  #    daemon's inheritance.
  environment=$(module_root_for "$directory")/last-environment.txt
  [ -f "$environment" ] || fail "the executor did not record its environment"
  if grep -q "$leak_probe_name" "$environment"; then
    printf 'executor environment:\n%s\n' "$(cat "$environment")" >&2
    fail "the daemon's own environment reached the Module command executor"
  fi
  if grep -q "$directory/workspace" "$environment"; then
    fail "the executor environment exposes the workspace path"
  fi

  # 7. The same key and the same body return the original job without running
  #    the command a second time.
  status=$(api_post "$directory" "$(invoke_path reindex)" \
    "{\"command_digest\":\"$normal_digest\",\"parameters\":{\"full\":true}}" \
    "$directory/reindex-retry.json" "key-reindex-1")
  expect_status "$status" 202 "idempotent retry" "$directory/reindex-retry.json"
  [ "$(job_id_from "$directory/reindex-retry.json")" = "$reindex_job" ] ||
    fail "an idempotent retry created a different job"
  sleep 1
  [ "$(invocation_count "$directory" reindex)" = "1" ] || fail "an idempotent retry ran the executor twice"

  # 8. Destructive: obtain a step-up bound to this command, spend it once.
  origin=$(origin_for_run)
  csrf=$(sed -n '1p' "$directory/csrf")
  password=$(sed -n '1p' "$directory/owner-password")
  status=$("$python_bin" -c \
    'import json,sys; print(json.dumps({"action":"module_command.invoke","workspace_id":"main","target_id":"demo.purge","password":sys.argv[1]}))' \
    "$password" |
    $curl_bin -ksS -o "$directory/step-up.json" -w '%{http_code}' \
      -c "$directory/cookies" -b "$directory/cookies" \
      -H 'Content-Type: application/json' -H "Origin: $origin" -H "X-CSRF-Token: $csrf" \
      --data-binary @- "$origin/api/v1/auth/step-up")
  password=
  expect_status "$status" 200 "module command step-up" "$directory/step-up.json"
  proof=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["proof"])' "$directory/step-up.json") ||
    fail "read the step-up proof"

  status=$(api_post "$directory" "$(invoke_path purge)" \
    "{\"command_digest\":\"$purge_digest\",\"step_up_proof\":\"$proof\"}" \
    "$directory/purge-accepted.json" "key-purge-1")
  expect_status "$status" 202 "destructive invoke with a proof" "$directory/purge-accepted.json"
  purge_job=$(job_id_from "$directory/purge-accepted.json")
  await_job "$directory" "$purge_job"
  [ "$(invocation_count "$directory" purge)" = "1" ] || fail "the purge executor did not run exactly once"

  # 9. The proof is single use: replaying it with a fresh key must be refused.
  status=$(api_post "$directory" "$(invoke_path purge)" \
    "{\"command_digest\":\"$purge_digest\",\"step_up_proof\":\"$proof\"}" \
    "$directory/purge-replay.json" "key-purge-2")
  expect_status "$status" 409 "step-up replay" "$directory/purge-replay.json"
  expect_problem_code "$directory/purge-replay.json" step_up_invalid "step-up replay"
  [ "$(invocation_count "$directory" purge)" = "1" ] || fail "a replayed proof ran the command again"

  # 10. Descriptor drift: a client holding a digest the active deployment no
  #     longer freezes is refused, and nothing runs.
  "$command_fixture" drift "$directory/workspace" >"$directory/descriptors-drift.json" ||
    fail "rewrite the frozen descriptor"
  chmod 0600 "$directory/descriptors-drift.json"
  status=$(api_post "$directory" "$(invoke_path reindex)" \
    "{\"command_digest\":\"$normal_digest\"}" "$directory/reindex-stale.json" "key-reindex-stale")
  expect_status "$status" 412 "stale digest invoke" "$directory/reindex-stale.json"
  expect_problem_code "$directory/reindex-stale.json" module_command_changed "stale digest invoke"
  [ "$(invocation_count "$directory" reindex)" = "1" ] || fail "a stale digest still ran the command"

  # 11. The invocations are in the audit journal, and the journal is read-only
  #     over the API.
  status=$(api_get "$directory" "/api/v1/audit-events?limit=100" "$directory/audit.json")
  expect_status "$status" 200 "audit query" "$directory/audit.json"
  "$python_bin" - "$directory/audit.json" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1], encoding="utf-8"))
# Deployment-family events all carry type "workspace.deployment"; the action is
# a detail. Asserting on the detail is what proves the sink accepted this action
# rather than failing closed and turning the route into a 503.
invokes = [
    item for item in body["items"]
    if item.get("type") == "workspace.deployment"
    and (item.get("details") or {}).get("action") == "module_command.invoke"
]
assert invokes, "no module_command.invoke audit record; actions seen: " + repr(
    sorted({(item.get("details") or {}).get("action") for item in body["items"]})
)
stages = sorted({item.get("outcome", "") for item in invokes})
assert "job_create_authorized" in stages, f"invoke audit stages = {stages}"
for item in invokes:
    assert "step_up_proof" not in json.dumps(item), "an audit record contains a raw proof"
print(f"audit_module_command_invoke_records={len(invokes)}")
print(f"audit_module_command_invoke_stages={stages}")
PY

  printf '%s\n' 'PASS: CONSOLE-R-181 CONSOLE-R-147 Module command invoke E2E'
  printf 'reindex_job=%s\n' "$reindex_job"
  printf 'purge_job=%s\n' "$purge_job"
  printf 'daemon=running\n'
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
  verify) verify_run ;;
  cleanup) cleanup_run ;;
  *)
    printf 'usage: %s {setup|verify|cleanup}\n' "$0" >&2
    exit 2
    ;;
esac
