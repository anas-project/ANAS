#!/usr/bin/env sh
# REQUIREMENTS: CONSOLE-R-063, CONSOLE-R-069
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

anas_bin=${ANAS_BIN:-}
anasd_bin=${ANASD_BIN:-}
module_root=${ANAS_MODULE_ROOT:-}
python_bin=${PYTHON:-$(command -v python3 || true)}
curl_bin=${CURL:-$(command -v curl || true)}
openssl_bin=${OPENSSL:-$(command -v openssl || true)}
daemon_log=
stage=preflight
failure_reported=false

fail() {
  failure_reported=true
  printf 'R-063/R-069 E2E failed: %s\n' "$1" >&2
  if [ -n "${daemon_log:-}" ] && [ -f "$daemon_log" ]; then
    printf '%s\n' '--- anasd log ---' >&2
    tail -n 120 "$daemon_log" >&2 || true
  fi
  exit 1
}

if [ "$(id -u)" -ne 0 ]; then
  fail "run as root so anasd can enforce production config and TLS file policy"
fi
for item in "$anas_bin" "$anasd_bin"; do
  [ -n "$item" ] && [ -f "$item" ] && [ ! -L "$item" ] && [ -x "$item" ] ||
    fail "ANAS_BIN and ANASD_BIN must name executable regular files"
done
[ -n "$module_root" ] && [ -d "$module_root" ] && [ ! -L "$module_root" ] ||
  fail "ANAS_MODULE_ROOT must name an unpacked Module directory"
[ -n "$python_bin" ] || fail "python3 is required"
[ -n "$curl_bin" ] || fail "curl is required"
[ -n "$openssl_bin" ] || fail "openssl is required"

workdir=$(mktemp -d /tmp/anas-r063-r069.XXXXXX)
workspace=$workdir/workspace
console_store=$workdir/console-store
certdir=$workdir/tls
service_config=$workdir/anasd.yml
bootstrap_jar=$workdir/bootstrap-cookies.txt
enrollment_jar=$workdir/enrollment-cookies.txt
local_owner_jar=$workdir/local-owner-cookies.txt
daemon_log=$workdir/anasd.log
daemon_pid=

cleanup() {
  cleanup_status=$?
  trap - EXIT HUP INT TERM
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
  if [ "$cleanup_status" -ne 0 ] && [ "$failure_reported" != true ]; then
    printf 'R-063/R-069 E2E failed unexpectedly during stage: %s (exit %s)\n' \
      "$stage" "$cleanup_status" >&2
    if [ -n "${daemon_log:-}" ] && [ -f "$daemon_log" ]; then
      printf '%s\n' '--- anasd log ---' >&2
      tail -n 120 "$daemon_log" >&2 || true
    fi
  fi
  case "$workdir" in
    /tmp/anas-r063-r069.*) rm -rf -- "$workdir" ;;
    *) printf 'refusing to clean unexpected path: %s\n' "$workdir" >&2 ;;
  esac
  exit "$cleanup_status"
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
base_domain=example.test
tls_host=anas.$base_domain
plain_origin=http://127.0.0.1:$port
tls_origin=https://$tls_host:$port

"$anas_bin" init "$workspace" -y >/dev/null 2>&1 || fail "initialize isolated workspace"
"$python_bin" - "$workspace/.anas/module-view.json" "$module_root" <<'PY'
import json
import os
import sys

target, module_root = sys.argv[1:]
with open(target, "w", encoding="utf-8") as stream:
    json.dump({
        "api_version": "anas.module-view/v1",
        "digest": "r063-r069-e2e",
        "module_root": os.path.abspath(module_root),
        "installations": {},
    }, stream, indent=2)
    stream.write("\n")
os.chmod(target, 0o600)
PY
"$anas_bin" config set -w "$workspace" --root "$module_root" --defer global.virtual_domain true --json >/dev/null ||
  fail "enable virtual_domain in isolated workspace"
"$python_bin" - "$workspace/config.yml" "$base_domain" <<'PY'
import re
import sys

body = open(sys.argv[1], encoding="utf-8").read()
assert re.search(r'^\s*virtual_domain:\s*["\']?true["\']?\s*$', body, re.MULTILINE), body
assert re.search(rf'^\s*base_domain:\s*{re.escape(sys.argv[2])}\s*$', body, re.MULTILINE), body
PY

mkdir -p "$certdir"
chmod 0700 "$certdir"
{
  printf '%s\n' 'api_version: anas.console-config/v1'
  printf '%s\n' 'mode: loopback'
  printf 'port: %s\n' "$port"
  printf '%s\n' 'allowed_dns_hosts:'
  printf '  - %s\n' "$tls_host"
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

token_document=$("$anas_bin" console token --config "$service_config" --ttl 15m --json) ||
  fail "issue bootstrap token"
bootstrap_token=$(printf '%s' "$token_document" | "$python_bin" -c 'import json,sys; print(json.load(sys.stdin)["token"])')
[ -n "$bootstrap_token" ] || fail "bootstrap token output was empty"

"$anasd_bin" --config "$service_config" >"$daemon_log" 2>&1 &
daemon_pid=$!

stage=bootstrap_exchange
csrf_body=$workdir/bootstrap-csrf.json
attempt=0
while :; do
  status=$("$curl_bin" -sS -c "$bootstrap_jar" -o "$csrf_body" -w '%{http_code}' \
    "$plain_origin/api/v1/auth/csrf" 2>/dev/null || true)
  [ "$status" = 200 ] && break
  kill -0 "$daemon_pid" 2>/dev/null || fail "anasd exited before accepting plaintext bootstrap requests"
  attempt=$((attempt + 1))
  [ "$attempt" -lt 100 ] || fail "anasd did not become ready"
  sleep 0.1
done
preauth_csrf=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["csrf_token"])' "$csrf_body")
exchange_request=$workdir/bootstrap-exchange-request.json
BOOTSTRAP_TOKEN=$bootstrap_token "$python_bin" - "$exchange_request" <<'PY'
import json
import os
import sys

with open(sys.argv[1], "w", encoding="utf-8") as stream:
    json.dump({"token": os.environ["BOOTSTRAP_TOKEN"]}, stream, separators=(",", ":"))
PY
chmod 0600 "$exchange_request"
exchange_body=$workdir/bootstrap-exchange.json
exchange_status=$("$curl_bin" -sS -b "$bootstrap_jar" -c "$bootstrap_jar" -o "$exchange_body" -w '%{http_code}' \
  -H "Origin: $plain_origin" -H "X-CSRF-Token: $preauth_csrf" -H 'Content-Type: application/json' \
  --data-binary "@$exchange_request" "$plain_origin/api/v1/auth/bootstrap/exchange") ||
  fail "bootstrap exchange transport failed"
[ "$exchange_status" = 200 ] || fail "bootstrap exchange returned HTTP $exchange_status"
bootstrap_csrf=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["csrf_token"])' "$exchange_body")
bootstrap_token=
token_document=
: >"$exchange_request"

stage=internal_certificate_install
"$openssl_bin" req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -sha256 -days 7 \
  -subj '/CN=ANAS R-069 Internal Root' \
  -addext 'basicConstraints=critical,CA:TRUE' \
  -addext 'keyUsage=critical,keyCertSign,cRLSign' \
  -keyout "$certdir/root.key" -out "$certdir/root.crt" >/dev/null 2>&1 ||
  fail "generate internal root CA"
chmod 0600 "$certdir/root.key"
chmod 0644 "$certdir/root.crt"
cat >"$certdir/leaf.ext" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=serverAuth
subjectAltName=DNS:$base_domain,DNS:$tls_host
EOF
chmod 0600 "$certdir/leaf.ext"

issue_leaf() {
  prefix=$1
  "$openssl_bin" req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -sha256 \
    -subj "/CN=$tls_host" -keyout "$certdir/$prefix.key" -out "$certdir/$prefix.csr" >/dev/null 2>&1 ||
    fail "generate $prefix private key and CSR"
  "$openssl_bin" x509 -req -sha256 -days 2 -in "$certdir/$prefix.csr" \
    -CA "$certdir/root.crt" -CAkey "$certdir/root.key" \
    -CAserial "$certdir/root.srl" -CAcreateserial -extfile "$certdir/leaf.ext" \
    -out "$certdir/$prefix.crt" >/dev/null 2>&1 || fail "sign $prefix certificate"
  chmod 0600 "$certdir/$prefix.key"
  chmod 0644 "$certdir/$prefix.crt"
}

certificate_spki() {
  "$openssl_bin" x509 -in "$1" -noout -pubkey |
    "$openssl_bin" pkey -pubin -outform DER 2>/dev/null |
    "$openssl_bin" dgst -sha256 -r | awk '{print $1}'
}

served_spki() {
  trust_root=$1
  "$openssl_bin" s_client -connect "127.0.0.1:$port" -servername "$tls_host" \
    -CAfile "$trust_root" -verify_return_error -no_ticket </dev/null 2>/dev/null |
    "$openssl_bin" x509 -noout -pubkey 2>/dev/null |
    "$openssl_bin" pkey -pubin -outform DER 2>/dev/null |
    "$openssl_bin" dgst -sha256 -r | awk '{print $1}'
}

issue_leaf leaf-one
install -m 0644 "$certdir/root.crt" "$certdir/issuer.crt.new"
install -m 0644 "$certdir/root.crt" "$certdir/trust.crt.new"
install -m 0644 "$certdir/root.crt" "$certdir/internal-ca.crt.new"
printf '%s\n' internal >"$certdir/issuer.new"
chmod 0644 "$certdir/issuer.new"
install -m 0644 "$certdir/leaf-one.crt" "$certdir/serving.crt.new"
install -m 0600 "$certdir/leaf-one.key" "$certdir/serving.key.new"
mv "$certdir/issuer.crt.new" "$certdir/issuer.crt"
mv "$certdir/trust.crt.new" "$certdir/trust.crt"
mv "$certdir/internal-ca.crt.new" "$certdir/internal-ca.crt"
mv "$certdir/issuer.new" "$certdir/issuer"
mv "$certdir/serving.crt.new" "$certdir/serving.crt"
mv "$certdir/serving.key.new" "$certdir/serving.key"

downloaded_ca=$workdir/downloaded-ca.crt
stage=enrollment_transition
attempt=0
while :; do
  ca_status=$("$curl_bin" -sS -o "$downloaded_ca" -w '%{http_code}' \
    "$plain_origin/api/v1/system/ca" 2>/dev/null || true)
  [ "$ca_status" = 200 ] && break
  kill -0 "$daemon_pid" 2>/dev/null || fail "anasd exited while waiting for certificate enrollment"
  attempt=$((attempt + 1))
  [ "$attempt" -lt 100 ] || fail "validated internal CA did not advance console to enrollment"
  sleep 0.1
done
cmp -s "$downloaded_ca" "$certdir/root.crt" || fail "downloaded internal CA differs from configured trust root"

handoff_body=$workdir/handoff.json
stage=handoff_issue
handoff_status=$("$curl_bin" -sS -b "$bootstrap_jar" -o "$handoff_body" -w '%{http_code}' \
  -X POST -H "Origin: $plain_origin" -H "X-CSRF-Token: $bootstrap_csrf" \
  "$plain_origin/api/v1/auth/enrollment/handoffs") || fail "handoff issuance transport failed"
[ "$handoff_status" = 201 ] || fail "handoff issuance returned HTTP $handoff_status"
handoff=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["handoff"])' "$handoff_body")
target_origin=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["target_origin"])' "$handoff_body")
form_action=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["form_action"])' "$handoff_body")
[ "$target_origin" = "$tls_origin" ] || fail "handoff target origin was $target_origin"
[ "$form_action" = "$tls_origin/api/v1/auth/enrollment/exchange" ] || fail "handoff form action was $form_action"

handoff_form=$workdir/handoff.form
HANDOFF=$handoff "$python_bin" - "$handoff_form" <<'PY'
import os
import sys
import urllib.parse

with open(sys.argv[1], "w", encoding="ascii") as stream:
    stream.write(urllib.parse.urlencode({"handoff": os.environ["HANDOFF"]}))
PY
chmod 0600 "$handoff_form"
ambient_body=$workdir/ambient-rejection.json
stage=handoff_ambient_cookie_rejection
ambient_status=$("$curl_bin" -sS --resolve "$tls_host:$port:127.0.0.1" --cacert "$certdir/root.crt" \
  -o "$ambient_body" -w '%{http_code}' -H "Origin: $plain_origin" -H 'Cookie: ambient=value' \
  -H 'Content-Type: application/x-www-form-urlencoded' --data-binary "@$handoff_form" "$form_action") ||
  fail "TLS handoff exchange with ambient Cookie failed during transport"
[ "$ambient_status" = 400 ] || fail "handoff exchange with ambient Cookie returned HTTP $ambient_status"

enrollment_headers=$workdir/enrollment.headers
enrollment_body=$workdir/enrollment.body
stage=handoff_exchange
enrollment_status=$("$curl_bin" -sS --resolve "$tls_host:$port:127.0.0.1" --cacert "$certdir/root.crt" \
  -c "$enrollment_jar" -D "$enrollment_headers" -o "$enrollment_body" -w '%{http_code}' \
  -H "Origin: $plain_origin" -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-binary "@$handoff_form" "$form_action") || fail "TLS handoff exchange failed during transport"
[ "$enrollment_status" = 303 ] || fail "handoff exchange returned HTTP $enrollment_status"
[ ! -s "$enrollment_body" ] || fail "handoff exchange returned a response body"
location=$(sed -n 's/^[Ll]ocation:[[:space:]]*//p' "$enrollment_headers" | tr -d '\r' | tail -n 1)
[ "$location" = "$tls_origin/" ] || fail "handoff redirect location was $location"
grep -F '__Host-anas_enrollment_session=' "$enrollment_headers" | grep -F 'Secure' | grep -F 'HttpOnly' >/dev/null ||
  fail "enrollment session Cookie attributes are incomplete"
grep -F '__Host-anas_enrollment_csrf=' "$enrollment_headers" | grep -F 'Secure' >/dev/null ||
  fail "enrollment CSRF Cookie attributes are incomplete"
enrollment_csrf=$(awk '$6 == "__Host-anas_enrollment_csrf" {print $7}' "$enrollment_jar" | tail -n 1)
[ -n "$enrollment_csrf" ] || fail "enrollment CSRF Cookie was not stored"

"$python_bin" - "$console_store" "$handoff" "$enrollment_csrf" <<'PY'
import os
import sys

root, *secrets = sys.argv[1:]
for directory, _, names in os.walk(root):
    for name in names:
        path = os.path.join(directory, name)
        if os.path.islink(path) or not os.path.isfile(path):
            continue
        body = open(path, "rb").read()
        for secret in secrets:
            assert secret.encode() not in body, path
PY

owner_password='correct horse battery staple R069'
owner_request=$workdir/owner-request.json
OWNER_PASSWORD=$owner_password "$python_bin" - "$owner_request" <<'PY'
import json
import os
import sys

with open(sys.argv[1], "w", encoding="utf-8") as stream:
    json.dump({"password": os.environ["OWNER_PASSWORD"]}, stream, separators=(",", ":"))
PY
chmod 0600 "$owner_request"
owner_body=$workdir/owner.json
stage=owner_creation
owner_status=$("$curl_bin" -sS --resolve "$tls_host:$port:127.0.0.1" --cacert "$certdir/root.crt" \
  -b "$enrollment_jar" -c "$enrollment_jar" -o "$owner_body" -w '%{http_code}' \
  -H "Origin: $tls_origin" -H "X-CSRF-Token: $enrollment_csrf" -H 'Content-Type: application/json' \
  --data-binary "@$owner_request" "$tls_origin/api/v1/auth/enrollment/owner") ||
  fail "owner creation failed during transport"
[ "$owner_status" = 201 ] || fail "owner creation returned HTTP $owner_status"
"$python_bin" - "$owner_body" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1]))
assert body["state"] == "full", body
PY
"$python_bin" - "$console_store" "$owner_password" <<'PY'
import os
import sys

for directory, _, names in os.walk(sys.argv[1]):
    for name in names:
        path = os.path.join(directory, name)
        if os.path.islink(path) or not os.path.isfile(path):
            continue
        assert sys.argv[2].encode() not in open(path, "rb").read(), path
PY
stage=full_system_probe
plain_full_status=$("$curl_bin" -sS -o /dev/null -w '%{http_code}' "$plain_origin/api/v1/system") ||
  fail "full-state plaintext system probe failed during transport"
[ "$plain_full_status" = 404 ] || fail "full-state plaintext system route returned HTTP $plain_full_status"
system_body=$workdir/system.json
system_status=$("$curl_bin" -sS --resolve "$tls_host:$port:127.0.0.1" --cacert "$certdir/root.crt" \
  -o "$system_body" -w '%{http_code}' "$tls_origin/api/v1/system") ||
  fail "full-state TLS system probe failed during transport"
[ "$system_status" = 200 ] || fail "full-state TLS system route returned HTTP $system_status"
"$python_bin" - "$system_body" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1]))
assert body["certificate_issuer"] == "internal", body
assert body["workspace_ids"] == ["main"], body
PY

local_csrf_body=$workdir/local-csrf.json
stage=local_owner_login
local_csrf_status=$("$curl_bin" -sS --resolve "$tls_host:$port:127.0.0.1" --cacert "$certdir/root.crt" \
  -c "$local_owner_jar" -o "$local_csrf_body" -w '%{http_code}' "$tls_origin/api/v1/auth/csrf") ||
  fail "full-state local-login CSRF failed during transport"
[ "$local_csrf_status" = 200 ] || fail "full-state local-login CSRF returned HTTP $local_csrf_status"
local_preauth_csrf=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["csrf_token"])' "$local_csrf_body")
local_login_body=$workdir/local-login.json
local_login_status=$("$curl_bin" -sS --resolve "$tls_host:$port:127.0.0.1" --cacert "$certdir/root.crt" \
  -b "$local_owner_jar" -c "$local_owner_jar" -o "$local_login_body" -w '%{http_code}' \
  -H "Origin: $tls_origin" -H "X-CSRF-Token: $local_preauth_csrf" -H 'Content-Type: application/json' \
  --data-binary "@$owner_request" "$tls_origin/api/v1/auth/login") ||
  fail "new local owner login failed during transport"
[ "$local_login_status" = 200 ] || fail "new local owner login returned HTTP $local_login_status"
"$python_bin" - "$local_login_body" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1]))
assert body["state"] == "full", body
PY
owner_password=
: >"$owner_request"

stage=internal_leaf_rotation
first_expected_spki=$(certificate_spki "$certdir/leaf-one.crt") || fail "calculate initial internal leaf SPKI"
first_served_spki=$(served_spki "$certdir/root.crt") || fail "read initial served internal leaf SPKI"
[ -n "$first_served_spki" ] && [ "$first_served_spki" = "$first_expected_spki" ] ||
  fail "first TLS handshake did not serve the initial internal-CA leaf"
issue_leaf leaf-two
second_expected_spki=$(certificate_spki "$certdir/leaf-two.crt")
[ "$second_expected_spki" != "$first_expected_spki" ] || fail "rotated certificate reused the old SPKI"
sleep 1
install -m 0644 "$certdir/leaf-two.crt" "$certdir/serving.crt.next"
install -m 0600 "$certdir/leaf-two.key" "$certdir/serving.key.next"
mv "$certdir/serving.crt.next" "$certdir/serving.crt"
mv "$certdir/serving.key.next" "$certdir/serving.key"

second_served_spki=$(served_spki "$certdir/root.crt") || fail "read rotated internal leaf SPKI"
[ -n "$second_served_spki" ] && [ "$second_served_spki" = "$second_expected_spki" ] ||
  fail "next TLS handshake did not hot-reload the rotated leaf"

stage=acme_chain_generation
"$openssl_bin" req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -sha256 -days 7 \
  -subj '/CN=ANAS R-063 Public Root' \
  -addext 'basicConstraints=critical,CA:TRUE,pathlen:1' \
  -addext 'keyUsage=critical,keyCertSign,cRLSign' \
  -keyout "$certdir/public-root.key" -out "$certdir/public-root.crt" >/dev/null 2>&1 ||
  fail "generate ACME trust root fixture"
chmod 0600 "$certdir/public-root.key"
chmod 0644 "$certdir/public-root.crt"
cat >"$certdir/intermediate.ext" <<'EOF'
basicConstraints=critical,CA:TRUE,pathlen:0
keyUsage=critical,keyCertSign,cRLSign
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid,issuer
EOF
chmod 0600 "$certdir/intermediate.ext"
"$openssl_bin" req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -sha256 \
  -subj '/CN=ANAS R-063 ACME Intermediate' \
  -keyout "$certdir/acme-intermediate.key" -out "$certdir/acme-intermediate.csr" >/dev/null 2>&1 ||
  fail "generate ACME intermediate fixture"
"$openssl_bin" x509 -req -sha256 -days 4 -in "$certdir/acme-intermediate.csr" \
  -CA "$certdir/public-root.crt" -CAkey "$certdir/public-root.key" \
  -CAserial "$certdir/public-root.srl" -CAcreateserial -extfile "$certdir/intermediate.ext" \
  -out "$certdir/acme-intermediate.crt" >/dev/null 2>&1 || fail "sign ACME intermediate fixture"
chmod 0600 "$certdir/acme-intermediate.key"
chmod 0644 "$certdir/acme-intermediate.crt"
"$openssl_bin" req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -sha256 \
  -subj "/CN=$tls_host" -keyout "$certdir/acme-leaf.key" -out "$certdir/acme-leaf.csr" >/dev/null 2>&1 ||
  fail "generate ACME leaf fixture"
"$openssl_bin" x509 -req -sha256 -days 2 -in "$certdir/acme-leaf.csr" \
  -CA "$certdir/acme-intermediate.crt" -CAkey "$certdir/acme-intermediate.key" \
  -CAserial "$certdir/acme-intermediate.srl" -CAcreateserial -extfile "$certdir/leaf.ext" \
  -out "$certdir/acme-leaf.crt" >/dev/null 2>&1 || fail "sign ACME leaf fixture"
chmod 0600 "$certdir/acme-leaf.key"
chmod 0644 "$certdir/acme-leaf.crt"
acme_expected_spki=$(certificate_spki "$certdir/acme-leaf.crt")
[ "$acme_expected_spki" != "$second_expected_spki" ] || fail "ACME fixture reused the internal leaf SPKI"

sleep 1
install -m 0644 "$certdir/acme-leaf.crt" "$certdir/serving.crt.next"
install -m 0600 "$certdir/acme-leaf.key" "$certdir/serving.key.next"
install -m 0644 "$certdir/acme-intermediate.crt" "$certdir/issuer.crt.next"
cat "$certdir/public-root.crt" "$certdir/root.crt" >"$certdir/trust.crt.next"
chmod 0644 "$certdir/trust.crt.next"
printf '%s\n' acme >"$certdir/issuer.next"
chmod 0644 "$certdir/issuer.next"
mv "$certdir/serving.crt.next" "$certdir/serving.crt"
mv "$certdir/serving.key.next" "$certdir/serving.key"
mv "$certdir/issuer.crt.next" "$certdir/issuer.crt"
mv "$certdir/trust.crt.next" "$certdir/trust.crt"
mv "$certdir/issuer.next" "$certdir/issuer"

stage=acme_switch
acme_served_spki=$(served_spki "$certdir/public-root.crt") || fail "read served ACME leaf SPKI"
[ -n "$acme_served_spki" ] && [ "$acme_served_spki" = "$acme_expected_spki" ] ||
  fail "next TLS handshake did not hot-switch from the internal CA to ACME"
acme_system_body=$workdir/system-acme.json
acme_system_status=$("$curl_bin" -sS --resolve "$tls_host:$port:127.0.0.1" --cacert "$certdir/public-root.crt" \
  -o "$acme_system_body" -w '%{http_code}' "$tls_origin/api/v1/system") ||
  fail "ACME TLS system probe failed during transport"
[ "$acme_system_status" = 200 ] || fail "ACME TLS system route returned HTTP $acme_system_status"
"$python_bin" - "$acme_system_body" <<'PY'
import json
import sys

body = json.load(open(sys.argv[1]))
assert body["certificate_issuer"] == "acme", body
PY
acme_ca_body=$workdir/downloaded-ca-after-acme.crt
acme_ca_status=$("$curl_bin" -sS --resolve "$tls_host:$port:127.0.0.1" --cacert "$certdir/public-root.crt" \
  -b "$local_owner_jar" -o "$acme_ca_body" -w '%{http_code}' "$tls_origin/api/v1/system/ca") ||
  fail "owner CA download after ACME switch failed during transport"
[ "$acme_ca_status" = 200 ] || fail "owner CA download after ACME switch returned HTTP $acme_ca_status"
cmp -s "$acme_ca_body" "$certdir/root.crt" || fail "ACME switch discarded the stable internal CA download"
kill -0 "$daemon_pid" 2>/dev/null || fail "anasd restarted or exited during the flow"

stage=completed
printf 'environment=%s %s filesystem=%s openssl=%s\n' \
  "$(. /etc/os-release; printf '%s-%s' "$ID" "$VERSION_ID")" \
  "$(uname -m)" "$(stat -f -c %T "$workdir")" "$($openssl_bin version | awk '{print $2}')"
printf 'R-069 virtual_domain=true certificate=internal handoff=%s owner=%s final_state=full plaintext_full=%s\n' \
  "$handoff_status/$enrollment_status" "$owner_status" "$plain_full_status"
printf 'R-063 initial_spki=%s rotated_internal_spki=%s acme_spki=%s issuer=acme internal_ca_retained=yes daemon_pid=%s restart=no\n' \
  "$first_served_spki" "$second_served_spki" "$acme_served_spki" "$daemon_pid"
printf '%s\n' 'PASS: CONSOLE-R-063 internal/ACME hot reload and CONSOLE-R-069 internal-CA full enrollment'
