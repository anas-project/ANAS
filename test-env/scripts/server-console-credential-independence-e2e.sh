#!/usr/bin/env sh
# REQUIREMENTS: CONSOLE-R-102
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

anas_bin=${ANAS_BIN:-}
anasd_bin=${ANASD_BIN:-}
fixture_bin=${ANAS_AUTH_FIXTURE_BIN:-}
samba_image=${ANAS_SAMBA_IMAGE:-ghcr.io/anas-project/anas-samba-dc:4.23.6-r10}
samba_build_context=${ANAS_SAMBA_BUILD_CONTEXT:-}
samba_base_image=${ANAS_SAMBA_BASE_IMAGE:-}
python_bin=${PYTHON:-$(command -v python3 || true)}
curl_bin=${CURL:-$(command -v curl || true)}
openssl_bin=${OPENSSL:-$(command -v openssl || true)}
docker_bin=${DOCKER:-$(command -v docker || true)}
daemon_pid=
daemon_log=
container_name=
image_created=false
stage=preflight
failure_reported=false

fail() {
  failure_reported=true
  printf 'R-102 E2E failed: %s\n' "$1" >&2
  if [ -n "${daemon_log:-}" ] && [ -f "$daemon_log" ]; then
    printf '%s\n' '--- anasd log ---' >&2
    tail -n 120 "$daemon_log" >&2 || true
  fi
  if [ -n "${container_name:-}" ] && "$docker_bin" inspect "$container_name" >/dev/null 2>&1; then
    printf '%s\n' '--- Samba log ---' >&2
    "$docker_bin" logs --tail 160 "$container_name" >&2 || true
  fi
  exit 1
}

if [ "$(id -u)" -ne 0 ]; then
  fail "run as root so anasd can enforce production config and TLS file policy"
fi
for item in "$anas_bin" "$anasd_bin" "$fixture_bin"; do
  [ -n "$item" ] && [ -f "$item" ] && [ ! -L "$item" ] && [ -x "$item" ] ||
    fail "ANAS_BIN, ANASD_BIN and ANAS_AUTH_FIXTURE_BIN must name executable regular files"
done
for item in "$python_bin" "$curl_bin" "$openssl_bin" "$docker_bin"; do
  [ -n "$item" ] || fail "python3, curl, openssl and docker are required"
done

workdir=$(mktemp -d /tmp/anas-r102.XXXXXX)
workspace=$workdir/workspace
console_store=$workdir/console-store
tls_dir=$workdir/tls
service_config=$workdir/anasd.yml
daemon_log=$workdir/anasd.log
samba_env=$workdir/samba.env
samba_entrypoint=$workdir/samba-entrypoint.sh
samba_data=$workdir/samba-data
suffix=$(basename "$workdir" | sed 's/[^a-zA-Z0-9_.-]/_/g')
container_name=anas_r102_$suffix

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
  if [ -n "${container_name:-}" ] && "$docker_bin" inspect "$container_name" >/dev/null 2>&1; then
    "$docker_bin" rm -f "$container_name" >/dev/null 2>&1 || true
  fi
  if [ "$image_created" = true ]; then
    "$docker_bin" image rm "$samba_image" >/dev/null 2>&1 || true
  fi
  if [ "$cleanup_status" -ne 0 ] && [ "$failure_reported" != true ]; then
    printf 'R-102 E2E failed unexpectedly during stage: %s (exit %s)\n' "$stage" "$cleanup_status" >&2
    if [ -n "${daemon_log:-}" ] && [ -f "$daemon_log" ]; then
      printf '%s\n' '--- anasd log ---' >&2
      tail -n 120 "$daemon_log" >&2 || true
    fi
  fi
  case "$workdir" in
    /tmp/anas-r102.*) rm -rf -- "$workdir" ;;
    *) printf 'refusing to clean unexpected path: %s\n' "$workdir" >&2 ;;
  esac
  exit "$cleanup_status"
}
trap cleanup EXIT HUP INT TERM

secret_suffix=$($openssl_bin rand -hex 16)
owner_one=R102-Owner-First-$secret_suffix-Aa1!
owner_two=R102-Owner-Second-$secret_suffix-Bb2!
samba_one=R102-Samba-First-$secret_suffix-Cc3!
samba_two=R102-Samba-Second-$secret_suffix-Dd4!
[ "$owner_one" != "$samba_one" ] && [ "$owner_two" != "$samba_two" ] || fail "test credentials are not independent"

stage=console_setup
"$anas_bin" init "$workspace" -y >/dev/null 2>&1 || fail "initialize isolated workspace"
mkdir -m 0700 "$tls_dir"
"$openssl_bin" req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -x509 -sha256 -days 1 \
  -subj '/CN=127.0.0.1' -addext 'subjectAltName=IP:127.0.0.1' \
  -addext 'basicConstraints=critical,CA:FALSE' -addext 'keyUsage=critical,digitalSignature' \
  -addext 'extendedKeyUsage=serverAuth' \
  -keyout "$tls_dir/temp-console.key" -out "$tls_dir/temp-console.crt" >/dev/null 2>&1 ||
  fail "generate temporary console certificate"
chmod 0600 "$tls_dir/temp-console.key" "$tls_dir/temp-console.crt"
port=$(
  "$python_bin" - <<'PY'
import socket

sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()
PY
)
origin=https://127.0.0.1:$port
{
  printf '%s\n' 'api_version: anas.console-config/v1'
  printf '%s\n' 'mode: loopback'
  printf 'port: %s\n' "$port"
  printf 'console_store: %s\n' "$console_store"
  printf '%s\n' 'workspaces:'
  printf '%s\n' '  - id: main'
  printf '    path: %s\n' "$workspace"
  printf '%s\n' 'tls:'
  printf '%s\n' '  temporary:'
  printf '    certificate: %s\n' "$tls_dir/temp-console.crt"
  printf '    private_key: %s\n' "$tls_dir/temp-console.key"
  printf '%s\n' '    ip_addresses:'
  printf '%s\n' '      - 127.0.0.1'
} >"$service_config"
chmod 0600 "$service_config"
ANAS_E2E_OWNER_PASSWORD=$owner_one "$fixture_bin" seed-full "$console_store" >"$workdir/seed-full.json" ||
  fail "seed independent full-state owner"

"$anasd_bin" --config "$service_config" >"$daemon_log" 2>&1 &
daemon_pid=$!
attempt=0
while :; do
  ready_status=$("$curl_bin" -ksS -o "$workdir/ready.json" -w '%{http_code}' "$origin/api/v1/auth/csrf" 2>/dev/null || true)
  [ "$ready_status" = 200 ] && break
  kill -0 "$daemon_pid" 2>/dev/null || fail "anasd exited before accepting full-state TLS requests"
  attempt=$((attempt + 1))
  [ "$attempt" -lt 100 ] || fail "anasd did not become ready"
  sleep 0.1
done

login_status() {
  password=$1
  label=$2
  jar=$workdir/$label.cookies
  csrf_body=$workdir/$label.csrf.json
  request_body=$workdir/$label.login.json
  response_body=$workdir/$label.response.json
  csrf_status=$("$curl_bin" -ksS -c "$jar" -o "$csrf_body" -w '%{http_code}' "$origin/api/v1/auth/csrf") || return 1
  [ "$csrf_status" = 200 ] || return 1
  csrf=$("$python_bin" -c 'import json,sys; print(json.load(open(sys.argv[1]))["csrf_token"])' "$csrf_body") || return 1
  R102_PASSWORD=$password "$python_bin" - "$request_body" <<'PY'
import json
import os
import sys

with open(sys.argv[1], "w", encoding="utf-8") as stream:
    json.dump({"password": os.environ["R102_PASSWORD"]}, stream, separators=(",", ":"))
PY
  chmod 0600 "$request_body"
  "$curl_bin" -ksS -b "$jar" -c "$jar" -o "$response_body" -w '%{http_code}' \
    -H "Origin: $origin" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' \
    --data-binary "@$request_body" "$origin/api/v1/auth/login"
}

stage=samba_setup
mkdir -m 0700 "$samba_data"
if [ -n "$samba_build_context" ]; then
  [ -d "$samba_build_context" ] && [ ! -L "$samba_build_context" ] &&
    [ -f "$samba_build_context/Dockerfile" ] && [ ! -L "$samba_build_context/Dockerfile" ] ||
    fail "ANAS_SAMBA_BUILD_CONTEXT must name an unpacked Samba Docker build context"
  samba_image=anas-r102-samba:$suffix
  if [ -n "$samba_base_image" ]; then
    test_dockerfile=$workdir/Samba.Dockerfile
    ANAS_SAMBA_BASE_IMAGE=$samba_base_image "$python_bin" - "$samba_build_context/Dockerfile" "$test_dockerfile" <<'PY'
import os
import sys

source, target = sys.argv[1:]
body = open(source, encoding="utf-8").read()
prefix = "ARG DOCKER_HUB_REGISTRY=docker.io\nFROM ${DOCKER_HUB_REGISTRY}/library/ubuntu:resolute AS rootfs\n"
assert body.startswith(prefix), "unexpected Samba Dockerfile base declaration"
replacement = "ARG ANAS_SAMBA_BASE_IMAGE\nFROM ${ANAS_SAMBA_BASE_IMAGE} AS rootfs\n"
with open(target, "w", encoding="utf-8") as stream:
    stream.write(replacement + body[len(prefix):])
PY
    chmod 0600 "$test_dockerfile"
    "$docker_bin" build --pull --build-arg ANAS_SAMBA_BASE_IMAGE="$samba_base_image" \
      -f "$test_dockerfile" -t "$samba_image" "$samba_build_context" >/dev/null ||
      fail "build isolated Samba test image from configured base"
  else
    "$docker_bin" build --pull -t "$samba_image" "$samba_build_context" >/dev/null ||
      fail "build isolated Samba test image"
  fi
  image_created=true
elif ! "$docker_bin" image inspect "$samba_image" >/dev/null 2>&1; then
  "$docker_bin" pull "$samba_image" >/dev/null || fail "pull isolated Samba test image"
  image_created=true
fi
{
  printf '%s\n' 'SAMBA_DC_REALM=R102.TEST'
  printf '%s\n' 'SAMBA_DC_WORKGROUP=R102'
  printf '%s\n' 'SAMBA_DC_ADMINISTRATOR_PASSWORD=R102-Recovery-Only-Aa1!'
  printf '%s\n' 'SAMBA_DC_ADMIN_NAME=admin'
  printf 'SAMBA_DC_ADMIN_PASSWORD=%s\n' "$samba_one"
} >"$samba_env"
chmod 0600 "$samba_env"
cat >"$samba_entrypoint" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
rm -f /etc/samba/smb.conf
samba-tool domain provision --server-role=dc --use-rfc2307 \
  --realm="$SAMBA_DC_REALM" --domain="$SAMBA_DC_WORKGROUP" \
  --dns-backend=SAMBA_INTERNAL --adminpass="$SAMBA_DC_ADMINISTRATOR_PASSWORD" >/tmp/r102-provision.log
samba-tool user create "$SAMBA_DC_ADMIN_NAME" "$SAMBA_DC_ADMIN_PASSWORD" >/tmp/r102-user.log
mkdir -p /var/log/samba/cores
chmod 0700 /var/log/samba/cores
exec samba -i --no-process-group
SH
chmod 0700 "$samba_entrypoint"
"$docker_bin" run -d --name "$container_name" --network bridge --hostname r102-dc \
  --cap-add SYS_ADMIN \
  --env-file "$samba_env" --entrypoint /bin/bash \
  -v "$samba_data:/var/lib/samba" \
  -v "$samba_entrypoint:/r102-entrypoint.sh:ro" "$samba_image" /r102-entrypoint.sh >/dev/null ||
  fail "start isolated Samba DC"
attempt=0
while ! "$docker_bin" exec "$container_name" samba-tool user show admin >/dev/null 2>&1; do
  "$docker_bin" inspect -f '{{.State.Running}}' "$container_name" 2>/dev/null | grep -qx true ||
    fail "isolated Samba DC exited during initialization"
  attempt=$((attempt + 1))
  [ "$attempt" -lt 180 ] || fail "isolated Samba DC did not initialize"
  sleep 1
done

samba_accepts() {
  test_password=$1
  "$docker_bin" exec -e R102_TEST_PASSWORD="$test_password" "$container_name" /bin/bash -ceu \
    'smbclient -L //127.0.0.1 -U "${SAMBA_DC_WORKGROUP}\\${SAMBA_DC_ADMIN_NAME}%${R102_TEST_PASSWORD}" -m SMB3 >/dev/null 2>&1'
}

stage=baseline_independence
[ "$(login_status "$owner_one" owner-baseline)" = 200 ] || fail "initial local owner password did not authenticate"
[ "$(login_status "$samba_one" samba-not-owner)" = 401 ] || fail "Samba admin password authenticated as local owner"
samba_accepts "$samba_one" || fail "initial samba_dc.admin_password did not authenticate to Samba"
if samba_accepts "$owner_one"; then
  fail "local owner password authenticated as Samba admin"
fi
samba_pwd_last_set_before=$(
  "$docker_bin" exec "$container_name" samba-tool user show admin --attributes=pwdLastSet |
    awk -F': ' '$1 == "pwdLastSet" {print $2}'
)
[ -n "$samba_pwd_last_set_before" ] || fail "initial Samba pwdLastSet was unavailable"

stage=samba_rotation
"$docker_bin" exec -e R102_NEW_PASSWORD="$samba_two" "$container_name" /bin/bash -ceu \
  'samba-tool user setpassword "$SAMBA_DC_ADMIN_NAME" --newpassword="$R102_NEW_PASSWORD" >/dev/null' ||
  fail "rotate samba_dc.admin_password"
samba_pwd_last_set_after=$(
  "$docker_bin" exec "$container_name" samba-tool user show admin --attributes=pwdLastSet |
    awk -F': ' '$1 == "pwdLastSet" {print $2}'
)
[ -n "$samba_pwd_last_set_after" ] && [ "$samba_pwd_last_set_after" != "$samba_pwd_last_set_before" ] ||
  fail "Samba pwdLastSet did not change during rotation"
# AD may deliberately accept the immediately previous password for a short
# grace interval. The durable pwdLastSet change plus successful new-password
# authentication proves rotation without imposing a policy R-102 does not own.
samba_accepts "$samba_two" || fail "rotated Samba admin password did not authenticate"
[ "$(login_status "$owner_one" owner-after-samba)" = 200 ] ||
  fail "Samba password rotation affected local owner authentication"

stage=owner_rotation
ANAS_E2E_OWNER_PASSWORD=$owner_two "$fixture_bin" set-owner "$console_store" >"$workdir/set-owner.json" ||
  fail "rotate local owner password"
[ "$(login_status "$owner_one" old-owner)" = 401 ] || fail "old local owner password survived owner rotation"
[ "$(login_status "$owner_two" new-owner)" = 200 ] || fail "rotated local owner password did not authenticate"
samba_accepts "$samba_two" || fail "local owner rotation affected Samba admin authentication"
if samba_accepts "$owner_two"; then
  fail "rotated local owner password authenticated as Samba admin"
fi

stage=secret_boundary
if grep -R -F -q -e "$owner_one" -e "$owner_two" -e "$samba_one" -e "$samba_two" "$console_store"; then
  fail "credential plaintext entered console store"
fi
if "$docker_bin" inspect "$container_name" | grep -F -q -e "$owner_one" -e "$owner_two"; then
  fail "local owner plaintext entered Samba container configuration"
fi
if grep -F -q -e "$owner_one" -e "$owner_two" -e "$samba_one" -e "$samba_two" "$daemon_log"; then
  fail "credential plaintext entered daemon log"
fi

stage=completed
printf 'environment=%s %s backing_fs=%s docker=%s samba_image=%s\n' \
  "$(. /etc/os-release; printf '%s-%s' "$ID" "$VERSION_ID")" "$(uname -m)" \
  "$(stat -f -c %T "$workdir")" "$($docker_bin version --format '{{.Server.Version}}')" "$samba_image"
printf '%s\n' 'R-102 samba_rotation=pwdLastSet-changed/new-accepted owner_login_after_samba=accepted'
printf '%s\n' 'R-102 owner_rotation=old-rejected/new-accepted samba_login_after_owner=accepted cross_credentials=rejected'
printf '%s\n' 'PASS: CONSOLE-R-102 local owner and samba_dc.admin_password remain independent across both rotations'
