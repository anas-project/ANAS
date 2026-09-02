#!/usr/bin/env sh
# REQUIREMENTS: CONSOLE-R-093
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
umask 077

command_name=${1:-}
anas_bin=${ANAS_BIN:-}
anasd_bin=${ANASD_BIN:-}
tls_check_bin=${ANAS_TLS_CHECK_BIN:-}
browser_source_bin=${ANAS_BROWSER_SOURCE_BIN:-}
module_root=${ANAS_MODULE_ROOT:-}
python_bin=${PYTHON:-$(command -v python3 || true)}
curl_bin=${CURL:-$(command -v curl || true)}
openssl_bin=${OPENSSL:-$(command -v openssl || true)}
base_domain=${ANAS_E2E_BASE_DOMAIN:-}
tls_host=${ANAS_E2E_TLS_HOST:-}
source_host=${ANAS_E2E_SOURCE_HOST:-$tls_host}
source_scheme=${ANAS_E2E_SOURCE_SCHEME:-http}
tls_certificate=${ANAS_E2E_TLS_CERTIFICATE:-}
tls_private_key=${ANAS_E2E_TLS_PRIVATE_KEY:-}
tls_issuer=${ANAS_E2E_TLS_ISSUER:-}
browser_source_port=${ANAS_E2E_BROWSER_SOURCE_PORT:-}
daemon_log=

fail() {
  printf 'R-093 browser handoff E2E failed: %s\n' "$1" >&2
  if [ -n "$daemon_log" ] && [ -f "$daemon_log" ]; then
    printf '%s\n' '--- anasd log ---' >&2
    tail -n 120 "$daemon_log" >&2 || true
  fi
  exit 1
}

require_root() {
  [ "$(id -u)" -eq 0 ] || fail "run as root so anasd can enforce production file policy"
}

require_regular_file() {
  [ -n "$1" ] && [ -f "$1" ] && [ ! -L "$1" ] || fail "$2 must name a regular, non-symlink file"
}

validate_run_id() {
  case "$1" in
    ''|*[!A-Za-z0-9._-]*) fail "ANAS_E2E_RUN_ID must use only letters, digits, dot, underscore, or hyphen" ;;
  esac
}

workdir_for_run() {
  validate_run_id "$1"
  printf '/tmp/anas-r093.%s\n' "$1"
}

pid_matches_daemon() {
  pid=$1
  expected=$2
  [ -n "$pid" ] && [ -d "/proc/$pid" ] || return 1
  actual=$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)
  [ "$actual" = "$(readlink -f "$expected")" ]
}

normalize_certificate_pem() {
  target=$1
  shift
  "$python_bin" - "$target" "$@" <<'PY'
import os
import re
import sys

target, *sources = sys.argv[1:]
pattern = re.compile(rb"-----BEGIN CERTIFICATE-----\r?\n.*?-----END CERTIFICATE-----", re.DOTALL)
blocks = []
for source in sources:
    body = open(source, "rb").read()
    matches = pattern.findall(body)
    if not matches or pattern.sub(b"", body).strip():
        raise SystemExit(f"certificate source contains non-PEM data: {source}")
    blocks.extend(matches)
with open(target, "wb") as stream:
    for block in blocks:
        stream.write(block.replace(b"\r\n", b"\n"))
        stream.write(b"\n")
os.chmod(target, 0o644)
PY
}

install_lego_candidate() {
  "$openssl_bin" req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -sha256 -days 1 \
    -subj '/CN=ANAS R-093 Ephemeral Internal Root' \
    -addext 'basicConstraints=critical,CA:TRUE' \
    -addext 'keyUsage=critical,keyCertSign,cRLSign' \
    -keyout "$certdir/internal-ca.key" -out "$certdir/internal-ca.crt.new" >/dev/null 2>&1 ||
    fail "generate ephemeral internal CA"
  rm -f -- "$certdir/internal-ca.key"
  chmod 0644 "$certdir/internal-ca.crt.new"
  normalize_certificate_pem "$certdir/serving.crt.new" "$tls_certificate"
  install -m 0600 "$tls_private_key" "$certdir/serving.key.new"
  normalize_certificate_pem "$certdir/issuer.crt.new" "$tls_issuer"
  normalize_certificate_pem "$certdir/trust.crt.new" "$tls_issuer" "$certdir/internal-ca.crt.new"
  printf '%s\n' acme >"$certdir/issuer.new"
  chmod 0644 "$certdir/issuer.new"
  mv "$certdir/issuer.crt.new" "$certdir/issuer.crt"
  mv "$certdir/trust.crt.new" "$certdir/trust.crt"
  mv "$certdir/internal-ca.crt.new" "$certdir/internal-ca.crt"
  mv "$certdir/issuer.new" "$certdir/issuer"
  mv "$certdir/serving.crt.new" "$certdir/serving.crt"
  mv "$certdir/serving.key.new" "$certdir/serving.key"

  tls_check_output=$workdir/tls-check.json
  tls_check_error=$workdir/tls-check.log
  if ! "$tls_check_bin" \
    -certificate "$certdir/serving.crt" \
    -private-key "$certdir/serving.key" \
    -issuer "$certdir/issuer.crt" \
    -trust-bundle "$certdir/trust.crt" \
    -internal-ca "$certdir/internal-ca.crt" \
    -issuer-marker "$certdir/issuer" \
    -base-domain "$base_domain" >"$tls_check_output" 2>"$tls_check_error"; then
    sed -n '1,120p' "$tls_check_error" >&2 || true
    fail "validate installed lego certificate candidate"
  fi
  : >"$tls_check_error"
}

assert_lego_candidate_files() {
  for artifact in \
    "$certdir/serving.crt" "$certdir/serving.key" "$certdir/issuer.crt" \
    "$certdir/trust.crt" "$certdir/internal-ca.crt" "$certdir/issuer"; do
    [ -f "$artifact" ] && [ ! -L "$artifact" ] || fail "lego candidate artifact disappeared: $artifact"
  done
}

stop_daemon() {
  directory=$1
  pid_file=$directory/anasd.pid
  binary_file=$directory/anasd.path
  [ -f "$pid_file" ] && [ -f "$binary_file" ] || return 0
  pid=$(sed -n '1p' "$pid_file")
  expected=$(sed -n '1p' "$binary_file")
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

stop_browser_source() {
  directory=$1
  pid_file=$directory/browser-source.pid
  binary_file=$directory/browser-source.path
  [ -f "$pid_file" ] && [ -f "$binary_file" ] || return 0
  pid=$(sed -n '1p' "$pid_file")
  expected=$(sed -n '1p' "$binary_file")
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
    /tmp/anas-r093.*) ;;
    *) fail "refuse cleanup outside /tmp/anas-r093.*" ;;
  esac
  stop_browser_source "$directory"
  stop_daemon "$directory"
  rm -rf -- "$directory"
}

setup_run() {
  require_root
  run_id=${ANAS_E2E_RUN_ID:-}
  port=${ANAS_E2E_PORT:-}
  validate_run_id "$run_id"
  case "$port" in
    ''|*[!0-9]*) fail "ANAS_E2E_PORT must be a numeric high port" ;;
  esac
  [ "$port" -ge 1024 ] && [ "$port" -le 65535 ] || fail "ANAS_E2E_PORT must be between 1024 and 65535"
  case "$browser_source_port" in
    ''|*[!0-9]*) fail "ANAS_E2E_BROWSER_SOURCE_PORT must be a numeric high port" ;;
  esac
  [ "$browser_source_port" -ge 1024 ] && [ "$browser_source_port" -le 65535 ] ||
    fail "ANAS_E2E_BROWSER_SOURCE_PORT must be between 1024 and 65535"
  [ "$browser_source_port" -ne "$port" ] ||
    fail "ANAS_E2E_BROWSER_SOURCE_PORT must differ from ANAS_E2E_PORT"
  for item in "$anas_bin" "$anasd_bin" "$tls_check_bin" "$browser_source_bin"; do
    require_regular_file "$item" "ANAS_BIN, ANASD_BIN, ANAS_TLS_CHECK_BIN, and ANAS_BROWSER_SOURCE_BIN"
    [ -x "$item" ] ||
      fail "ANAS_BIN, ANASD_BIN, ANAS_TLS_CHECK_BIN, and ANAS_BROWSER_SOURCE_BIN must be executable"
  done
  [ -n "$module_root" ] && [ -d "$module_root" ] && [ ! -L "$module_root" ] ||
    fail "ANAS_MODULE_ROOT must name an unpacked Module directory"
  [ -n "$python_bin" ] || fail "python3 is required"
  [ -n "$curl_bin" ] || fail "curl is required"
  [ -n "$openssl_bin" ] || fail "openssl is required"
  for domain_name in "$base_domain" "$tls_host"; do
    case "$domain_name" in
      ''|*[!A-Za-z0-9.-]*) fail "ANAS_E2E_BASE_DOMAIN and ANAS_E2E_TLS_HOST must be DNS names" ;;
    esac
  done
  case "$source_host" in
    ''|*[!A-Za-z0-9.-]*) fail "ANAS_E2E_SOURCE_HOST must be a DNS name or IPv4 literal" ;;
  esac
  case "$source_scheme" in
    http|https) ;;
    *) fail "ANAS_E2E_SOURCE_SCHEME must be http or https" ;;
  esac
  require_regular_file "$tls_certificate" "ANAS_E2E_TLS_CERTIFICATE"
  require_regular_file "$tls_private_key" "ANAS_E2E_TLS_PRIVATE_KEY"
  require_regular_file "$tls_issuer" "ANAS_E2E_TLS_ISSUER"

  workdir=$(workdir_for_run "$run_id")
  [ ! -e "$workdir" ] || fail "test workdir already exists: $workdir"
  mkdir -m 0700 "$workdir"
  keep_run=false
  trap 'status=$?; trap - EXIT HUP INT TERM; if [ "$keep_run" != true ]; then cleanup_run "$workdir"; fi; exit "$status"' EXIT HUP INT TERM

  workspace=$workdir/workspace
  console_store=$workdir/console-store
  certdir=$workdir/tls
  service_config=$workdir/anasd.yml
  bootstrap_jar=$workdir/bootstrap-cookies.txt
  daemon_log=$workdir/anasd.log
  mkdir -m 0700 "$certdir"
  if [ "$source_scheme" = https ]; then
    install_lego_candidate
    assert_lego_candidate_files
  fi

  initial_config=$workdir/initial-config.yml
  {
    printf '%s\n' 'module_source: official'
    printf '%s\n' 'modules:'
    printf '%s\n' '  samba_dc:'
    printf '%s\n' '    config:'
    printf '      domain: %s\n' "$base_domain"
    printf '%s\n' 'global:'
    printf '  base_domain: %s\n' "$base_domain"
    printf '  email: admin@%s\n' "$base_domain"
    printf '%s\n' '  timezone: UTC'
    printf '%s\n' 'env:'
    printf '%s\n' '  CONTAINER_PREFIX: anas_r093_'
    printf '%s\n' '  NETWORK_PREFIX: anas_r093_'
  } >"$initial_config"
  chmod 0600 "$initial_config"
  init_output=$workdir/init.json
  if ! "$anas_bin" init "$workspace" --config "$initial_config" --module-root "$module_root" -y --json \
    >"$init_output" 2>&1; then
    sed -n '1,120p' "$init_output" >&2 || true
    fail "initialize isolated workspace"
  fi
  if [ "$source_scheme" = https ]; then
    assert_lego_candidate_files
  fi
  "$python_bin" - "$workspace/.anas/module-view.json" "$module_root" <<'PY'
import json
import os
import sys

target, module_root = sys.argv[1:]
with open(target, "w", encoding="utf-8") as stream:
    json.dump({
        "api_version": "anas.module-view/v1",
        "digest": "r093-browser-e2e",
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
    printf '%s\n' 'allowed_dns_hosts:'
    printf '  - %s\n' "$tls_host"
    if [ "$source_scheme" = https ] && [ "$source_host" != "$tls_host" ]; then
      printf '  - %s\n' "$source_host"
    fi
    printf 'console_store: %s\n' "$console_store"
    printf '%s\n' 'workspaces:'
    printf '%s\n' '  - id: main'
    printf '    path: %s\n' "$workspace"
    printf '%s\n' 'tls:'
    printf '%s\n' '  lego:'
    printf '    base_domain: %s\n' "$base_domain"
    printf '    certificate: %s\n' "$certdir/serving.crt"
    printf '    private_key: %s\n' "$certdir/serving.key"
    printf '    issuer: %s\n' "$certdir/issuer.crt"
    printf '    trust_bundle: %s\n' "$certdir/trust.crt"
    printf '    internal_ca: %s\n' "$certdir/internal-ca.crt"
    printf '    issuer_marker: %s\n' "$certdir/issuer"
  } >"$service_config"
  chmod 0600 "$service_config"

  token_document=$workdir/bootstrap-token.json
  token_error=$workdir/bootstrap-token.log
  if ! "$anas_bin" console token --config "$service_config" --ttl 15m --json \
    >"$token_document" 2>"$token_error"; then
    sed -n '1,120p' "$token_document" >&2 || true
    sed -n '1,120p' "$token_error" >&2 || true
    fail "issue bootstrap token"
  fi
  if [ "$source_scheme" = https ]; then
    assert_lego_candidate_files
  fi
  : >"$token_error"
  chmod 0600 "$token_document"
  bootstrap_token=$(
    "$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "$token_document"
  )
  [ -n "$bootstrap_token" ] || fail "bootstrap token output was empty"

  printf '%s\n' "$anasd_bin" >"$workdir/anasd.path"
  chmod 0600 "$workdir/anasd.path"
  nohup "$anasd_bin" --config "$service_config" </dev/null >"$daemon_log" 2>&1 &
  daemon_pid=$!
  printf '%s\n' "$daemon_pid" >"$workdir/anasd.pid"
  chmod 0600 "$workdir/anasd.pid"

  source_origin=$source_scheme://$source_host:$port
  target_origin=https://$tls_host:$port
  resolve=$source_host:$port:127.0.0.1
  csrf_body=$workdir/bootstrap-csrf.json
  attempt=0
  while :; do
    status=$(
      "$curl_bin" -sS --resolve "$resolve" -c "$bootstrap_jar" -o "$csrf_body" -w '%{http_code}' \
        "$source_origin/api/v1/auth/csrf" 2>/dev/null || true
    )
    [ "$status" = 200 ] && break
    pid_matches_daemon "$daemon_pid" "$anasd_bin" || fail "anasd exited before the bootstrap source became ready"
    attempt=$((attempt + 1))
    [ "$attempt" -lt 100 ] || fail "bootstrap source did not become ready"
    sleep 0.1
  done
  preauth_csrf=$(
    "$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["csrf_token"])' "$csrf_body"
  )
  exchange_request=$workdir/bootstrap-exchange.json
  BOOTSTRAP_TOKEN=$bootstrap_token "$python_bin" - "$exchange_request" <<'PY'
import json
import os
import sys

with open(sys.argv[1], "w", encoding="utf-8") as stream:
    json.dump({"token": os.environ["BOOTSTRAP_TOKEN"]}, stream, separators=(",", ":"))
PY
  chmod 0600 "$exchange_request"
  exchange_body=$workdir/bootstrap-session.json
  exchange_status=$(
    "$curl_bin" -sS --resolve "$resolve" -b "$bootstrap_jar" -c "$bootstrap_jar" \
      -o "$exchange_body" -w '%{http_code}' \
      -H "Origin: $source_origin" -H "X-CSRF-Token: $preauth_csrf" \
      -H 'Content-Type: application/json' --data-binary "@$exchange_request" \
      "$source_origin/api/v1/auth/bootstrap/exchange"
  ) || fail "bootstrap exchange transport failed"
  [ "$exchange_status" = 200 ] || fail "bootstrap exchange returned HTTP $exchange_status"
  bootstrap_csrf=$(
    "$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["csrf_token"])' "$exchange_body"
  )
  bootstrap_token=
  : >"$token_document"
  : >"$exchange_request"

  if [ "$source_scheme" = http ]; then
    install_lego_candidate
  fi

  handoff_document=$workdir/browser-handoff.json
  attempt=0
  while :; do
    handoff_status=$(
      "$curl_bin" -sS --resolve "$resolve" -b "$bootstrap_jar" -o "$handoff_document" -w '%{http_code}' \
        -X POST -H "Origin: $source_origin" -H "X-CSRF-Token: $bootstrap_csrf" \
        "$source_origin/api/v1/auth/enrollment/handoffs" 2>/dev/null || true
    )
    [ "$handoff_status" = 201 ] && break
    pid_matches_daemon "$daemon_pid" "$anasd_bin" || fail "anasd exited while waiting for enrollment"
    attempt=$((attempt + 1))
    [ "$attempt" -lt 150 ] || fail "validated ACME certificate did not enable handoff issuance (last HTTP $handoff_status)"
    sleep 0.1
  done
  chmod 0600 "$handoff_document"

  "$python_bin" - "$handoff_document" "$source_origin" "$target_origin" <<'PY'
import json
import sys

path, source_origin, target_origin = sys.argv[1:]
body = json.load(open(path, encoding="utf-8"))
assert body["handoff"]
assert body["target_origin"] == target_origin, body
assert body["form_action"] == target_origin + "/api/v1/auth/enrollment/exchange", body
PY

  expected_spki=$(
    "$openssl_bin" x509 -in "$certdir/serving.crt" -noout -pubkey |
      "$openssl_bin" pkey -pubin -outform DER 2>/dev/null |
      "$openssl_bin" dgst -sha256 -r | awk '{print $1}'
  )
  [ -n "$expected_spki" ] || fail "calculate serving certificate SPKI"

  reconnect_output=$workdir/tls-reconnect.txt
  "$openssl_bin" s_client -connect "127.0.0.1:$port" -servername "$tls_host" -reconnect \
    </dev/null >"$reconnect_output" 2>/dev/null || fail "exercise repeated TLS handshakes"
  if grep -q '^Reused,' "$reconnect_output"; then
    fail "direct TLS unexpectedly resumed a session"
  fi
  : >"$reconnect_output"

  browser_source_log=$workdir/browser-source.log
  printf '%s\n' "$browser_source_bin" >"$workdir/browser-source.path"
  chmod 0600 "$workdir/browser-source.path"
  nohup "$browser_source_bin" \
    -listen "127.0.0.1:$browser_source_port" \
    -certificate "$certdir/serving.crt" \
    -private-key "$certdir/serving.key" \
    -action "$target_origin/api/v1/auth/enrollment/exchange" \
    </dev/null >"$browser_source_log" 2>&1 &
  browser_source_pid=$!
  printf '%s\n' "$browser_source_pid" >"$workdir/browser-source.pid"
  chmod 0600 "$workdir/browser-source.pid"
  browser_source_body=$workdir/browser-source.html
  attempt=0
  while :; do
    browser_source_status=$(
      "$curl_bin" -sS --resolve "$source_host:$browser_source_port:127.0.0.1" \
        -o "$browser_source_body" -w '%{http_code}' \
        "https://$source_host:$browser_source_port/" 2>/dev/null || true
    )
    [ "$browser_source_status" = 200 ] && break
    pid_matches_daemon "$browser_source_pid" "$browser_source_bin" || {
      sed -n '1,80p' "$browser_source_log" >&2 || true
      fail "browser source exited before becoming ready"
    }
    attempt=$((attempt + 1))
    [ "$attempt" -lt 100 ] || fail "browser source did not become ready"
    sleep 0.1
  done
  "$python_bin" - "$browser_source_body" "$handoff_document" "$target_origin" <<'PY'
import json
import sys

page_path, handoff_path, target_origin = sys.argv[1:]
page = open(page_path, encoding="utf-8").read()
handoff = json.load(open(handoff_path, encoding="utf-8"))["handoff"]
assert 'method="post"' in page
assert 'name="handoff"' in page
assert f'action="{target_origin}/api/v1/auth/enrollment/exchange"' in page
assert handoff not in page
PY
  : >"$browser_source_body"

  "$python_bin" - "$console_store" "$daemon_log" "$handoff_document" <<'PY'
import json
import os
import sys

store, daemon_log, document = sys.argv[1:]
handoff = json.load(open(document, encoding="utf-8"))["handoff"].encode()
for root, _, files in os.walk(store):
    for name in files:
        if handoff in open(os.path.join(root, name), "rb").read():
            raise SystemExit("handoff plaintext entered console store before browser exchange")
if handoff in open(daemon_log, "rb").read():
    raise SystemExit("handoff plaintext entered daemon log before browser exchange")
PY

  metadata=$workdir/metadata.json
  "$python_bin" - "$metadata" "$workdir" "$source_origin" "$target_origin" "$expected_spki" "$port" "$browser_source_port" <<'PY'
import json
import os
import sys

path, workdir, source_origin, target_origin, spki, port, browser_source_port = sys.argv[1:]
with open(path, "w", encoding="utf-8") as stream:
    json.dump({
        "workdir": workdir,
        "handoff_document": os.path.join(workdir, "browser-handoff.json"),
        "browser_source_url": source_origin + "/",
        "browser_source_backend_port": int(browser_source_port),
        "source_origin": source_origin,
        "target_origin": target_origin,
        "form_action": target_origin + "/api/v1/auth/enrollment/exchange",
        "expected_spki_sha256": spki,
        "port": int(port),
    }, stream, separators=(",", ":"))
    stream.write("\n")
os.chmod(path, 0o600)
PY

  keep_run=true
  trap - EXIT HUP INT TERM
  printf 'workdir=%s\n' "$workdir"
  printf 'metadata=%s\n' "$metadata"
  printf 'handoff_document=%s\n' "$handoff_document"
  printf 'source_origin=%s\n' "$source_origin"
  printf 'target_origin=%s\n' "$target_origin"
  printf 'browser_source_url=%s/\n' "$source_origin"
  printf 'browser_source_backend=127.0.0.1:%s\n' "$browser_source_port"
  printf 'expected_spki_sha256=%s\n' "$expected_spki"
  printf '%s\n' 'ready=browser_form_post'
}

verify_run() {
  require_root
  run_id=${ANAS_E2E_RUN_ID:-}
  workdir=$(workdir_for_run "$run_id")
  [ -d "$workdir" ] || fail "test workdir does not exist: $workdir"
  handoff_document=$workdir/browser-handoff.json
  [ -f "$handoff_document" ] || fail "browser handoff document is missing"
  console_store=$workdir/console-store
  daemon_log=$workdir/anasd.log

  "$python_bin" - "$console_store" "$daemon_log" "$handoff_document" <<'PY'
import json
import os
import sys

store, daemon_log, document = sys.argv[1:]
body = json.load(open(document, encoding="utf-8"))
handoff = body["handoff"].encode()
state_path = os.path.join(store, "bootstrap.json")
state = json.load(open(state_path, encoding="utf-8"))
assert state.get("enrollment_handoff") is None, state
sessions = state.get("enrollment_sessions", {})
assert len(sessions) == 1, state
session = next(iter(sessions.values()))
assert session["origin"] == body["target_origin"], (session, body)
for root, _, files in os.walk(store):
    for name in files:
        if handoff in open(os.path.join(root, name), "rb").read():
            raise SystemExit("handoff plaintext entered console store")
if handoff in open(daemon_log, "rb").read():
    raise SystemExit("handoff plaintext entered daemon log")
PY

  rm -f -- "$handoff_document"
  printf '%s\n' 'PASS: CONSOLE-R-093 browser handoff consumed into one target-origin enrollment session'
  printf '%s\n' 'handoff_plaintext=absent-from-console-store-and-daemon-log'
  printf '%s\n' 'remote_handoff_fixture=removed'
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
