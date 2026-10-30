#!/usr/bin/env bash
# TEST_CASES: MAILMOD-T-076
# Run as root only on an explicitly isolated test host. A private mount
# namespace replaces /etc, /var/lib and /usr/local/libexec for child processes.
set -euo pipefail
if [[ ${1:-} != --inner ]]; then
  [[ $(id -u) = 0 && $# = 1 && -f $1 && -x $1 ]] || exit 2
  base=$(mktemp -d /tmp/anas-access-isolated.XXXXXXXX)
  cleanup() { rm -rf -- "$base"; }
  trap cleanup EXIT
  mkdir -p "$base/etc/ssh" "$base/etc/sudoers.d" "$base/varlib" "$base/libexec"
  chmod 0755 "$base/etc" "$base/etc/ssh" "$base/varlib" "$base/libexec"
  chmod 0750 "$base/etc/sudoers.d"
  cp -a /etc/pam.d "$base/etc/pam.d"
  if [[ ${ANAS_ACCESS_REAL_PAM:-0} = 1 ]]; then
    # Copy public PAM configuration, never the host password database, keys,
    # or sudo rules. Runtime services such as logind remain host services.
    mkdir -p "$base/etc/security" "$base/etc/default"
    for config in limits.conf pam_env.conf access.conf time.conf group.conf namespace.conf faillock.conf; do
      [[ ! -f /etc/security/$config ]] || cp "/etc/security/$config" "$base/etc/security/$config"
    done
    [[ ! -d /etc/security/limits.d ]] || cp -a /etc/security/limits.d "$base/etc/security/limits.d"
    [[ ! -f /etc/default/locale ]] || cp /etc/default/locale "$base/etc/default/locale"
    [[ ! -f /etc/environment ]] || cp /etc/environment "$base/etc/environment"
    [[ ! -f /etc/shells ]] || cp /etc/shells "$base/etc/shells"
  else
    # The fixture replaces /etc, so use a minimal isolated account/session stack.
    for service in sshd sudo; do
      cat > "$base/etc/pam.d/$service" <<'PAM'
auth required pam_permit.so
account required pam_permit.so
session required pam_permit.so
PAM
    done
  fi
  [[ ! -f /etc/login.defs ]] || cp /etc/login.defs "$base/etc/login.defs"
  cat > "$base/etc/passwd" <<'PASSWD'
root:x:0:0:root:/root:/bin/bash
sshd:x:74:74:sshd:/run/sshd:/usr/sbin/nologin
PASSWD
  cat > "$base/etc/group" <<'GROUP'
root:x:0:
sshd:x:74:
GROUP
  cat > "$base/etc/shadow" <<'SHADOW'
root:*:19000:0:99999:7:::
sshd:*:19000:0:99999:7:::
SHADOW
  chmod 0600 "$base/etc/shadow"
  cat > "$base/etc/gshadow" <<'GSHADOW'
root:!::
sshd:!::
GSHADOW
  chmod 0600 "$base/etc/gshadow"
  cat > "$base/etc/nsswitch.conf" <<'NSS'
passwd: files
group: files
shadow: files
hosts: files dns
sudoers: files
NSS
  cat > "$base/etc/sudoers" <<'SUDOERS'
Defaults env_reset
root ALL=(ALL:ALL) ALL
@includedir /etc/sudoers.d
SUDOERS
  chmod 0440 "$base/etc/sudoers"
  cp "$1" "$base/cloudrelay"
  cp "$0" "$base/test.sh"
  if [[ -n ${ANAS_ACCESS_SSH_TEST_BIN:-} ]]; then
    cp "$ANAS_ACCESS_SSH_TEST_BIN" "$base/ssh-client-test"
    chmod 0755 "$base/ssh-client-test"
  fi
  chmod 0755 "$base/cloudrelay" "$base/test.sh"
  ssh-keygen -q -t ed25519 -N '' -f "$base/hostkey"
  ssh-keygen -q -t ed25519 -N '' -f "$base/client"
  cp "$base/client.pub" "$base/etc/test-client.pub"
  port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
  cat > "$base/etc/ssh/sshd_config" <<CONFIG
Port $port
ListenAddress 127.0.0.1
HostKey $base/hostkey
PidFile $base/sshd.pid
AuthorizedKeysFile .ssh/authorized_keys .ssh/authorized_keys2
StrictModes yes
PubkeyAuthentication yes
PasswordAuthentication no
KbdInteractiveAuthentication no
GSSAPIAuthentication no
HostbasedAuthentication no
PermitUserEnvironment no
PermitEmptyPasswords no
AuthenticationMethods publickey
UsePAM yes
AllowUsers anasmaildeploy
PermitRootLogin no
PermitTTY no
PrintMotd no
DisableForwarding yes
LogLevel VERBOSE
CONFIG
  printf '[127.0.0.1]:%s ' "$port" > "$base/known_hosts"
  cat "$base/hostkey.pub" >> "$base/known_hosts"
  printf 'fixture=%s port=%s\n' "$base" "$port"
  unshare --mount --fork "$base/test.sh" --inner "$base" "$port"
  echo 'private mount namespace test completed'
  exit 0
fi
[[ $(id -u) = 0 && $# = 3 && $2 == /tmp/anas-access-isolated.* ]] || exit 2
base=$2
port=$3
mount --make-rprivate /
mount --bind "$base/etc" /etc
mount --bind "$base/varlib" /var/lib
mount --bind "$base/libexec" /usr/local/libexec
# Parent and test files are root-controlled; public key is accepted by the CLI.
/usr/sbin/sshd -T -C "user=anasmaildeploy,addr=127.0.0.1,host=localhost,laddr=127.0.0.1,lport=$port" | grep -E '^(pubkeyauthentication|passwordauthentication|kbdinteractiveauthentication|gssapiauthentication|hostbasedauthentication|authorizedkeyscommand|trustedusercakeys|permituserenvironment|permitemptypasswords|strictmodes|usepam|forcecommand|pubkeyacceptedalgorithms|authorizedkeysfile|authenticationmethods) '
"$base/cloudrelay" bootstrap-sshd-check 127.0.0.1 localhost 127.0.0.1 "$port"
if "$base/cloudrelay" bootstrap-install-access "$base/client.pub" 127.0.0.1 localhost 127.0.0.1 "$port"; then echo 'public key under writable ancestor accepted' >&2; exit 1; fi
[[ ! -e /etc/anas-mail-relay-bootstrap ]]
"$base/cloudrelay" publish-bootstrap
"$base/cloudrelay" bootstrap-install-access /etc/test-client.pub 127.0.0.1 localhost 127.0.0.1 "$port"
"$base/cloudrelay" bootstrap-install-access /etc/test-client.pub 127.0.0.1 localhost 127.0.0.1 "$port"
getent passwd anasmaildeploy >/dev/null
[[ $(stat -c '%a' /etc/sudoers.d/anas-mail-relay-bootstrap) = 440 ]]
[[ $(stat -c '%a' /var/lib/anas-mail-relay-bootstrap/.ssh/authorized_keys) = 644 ]]
# sshd -D stays in this namespace, listens only on a random loopback port, and
# is stopped by the shell that spawned it on success or failure.
/usr/sbin/sshd -t -f /etc/ssh/sshd_config
/usr/sbin/sshd -D -e -f /etc/ssh/sshd_config >"$base/sshd.log" 2>&1 &
sshd_pid=$!
cleanup_sshd() { kill "$sshd_pid" 2>/dev/null || true; wait "$sshd_pid" 2>/dev/null || true; }
trap cleanup_sshd EXIT
for _ in {1..100}; do
  if (echo > /dev/tcp/127.0.0.1/"$port") 2>/dev/null; then break; fi
  kill -0 "$sshd_pid"
  sleep .05
done
# Valid protocol reaches the actual restricted helper and reports conflict for
# an intentionally absent relay. No runtime config or service is installed.
python3 - > "$base/inspect.json" <<'PY'
import json
c={"schema":"anas.mail-vps-runtime/v1","directory":"/var/lib/anas-mail-relay/data/source","source_id":"11111111-1111-4111-8111-111111111111","generation":"22222222-2222-4222-8222-222222222222","domains":["example.test"],"capacity_bytes":8388608,"delivered_days":7,"listen":"127.0.0.1:18448","certificate_host":"127.0.0.1","reader":"a"*64,"control":"b"*64,"tls_certificate":"","tls_private_key":"","notification_origin":"","notification_secret":"","notification_ca":"","smtp_listen":"127.0.0.1:18449","smtp_hostname":"relay.example.test"}
print(json.dumps({"schema":"anas.mail-systemd-bootstrap/v1","action":"inspect","config":c}))
PY
set +e
ssh -F /dev/null -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$base/known_hosts" -o LogLevel=ERROR -i "$base/client" -p "$port" anasmaildeploy@127.0.0.1 id < "$base/inspect.json" > "$base/output" 2> "$base/error"
result=$?
set -e
if [[ $result = 0 ]] || ! grep -qx '{"state":"conflict"}' "$base/output"; then
  echo 'restricted SSH helper did not return expected conflict' >&2
  echo 'STDOUT:' >&2
  cat "$base/output" >&2
  echo 'STDERR:' >&2
  cat "$base/error" >&2
  echo 'SSHD:' >&2
  cat "$base/sshd.log" >&2
  exit 1
fi
echo 'restricted SSH reached fixed helper, arbitrary command ignored'
if [[ -f "$base/ssh-client-test" ]]; then
  ANAS_TEST_VPS_SSH_PORT="$port" ANAS_TEST_VPS_SSH_USER=anasmaildeploy ANAS_TEST_VPS_SSH_KEY="$base/client" ANAS_TEST_VPS_SSH_HOSTKEY="$base/hostkey.pub" "$base/ssh-client-test" -test.v -test.run '^TestRelaySSHRealRestrictedInspect$'
fi
if [[ ${ANAS_ACCESS_CROSSHOST:-0} = 1 ]]; then
  printf 'CROSSHOST_READY %s %s\n' "$port" "$base"
  # SSH stdin is a lease held by the controller. EOF aborts the fixture and
  # kills this isolated sshd rather than leaving an exposed listener behind.
  IFS= read -r step
  [[ $step = publish ]]
  mkdir -p /etc/systemd/system
  printf 'CROSSHOST_PUBLISH_READY\n'
  IFS= read -r release
  [[ $release = finished ]]
  [[ -f /etc/anas-mail-relay/runtime.json && -f /etc/systemd/system/anas-mail-relay.service ]]
  cmp -s "$base/cloudrelay" /usr/local/libexec/anas-mail-relay/cloudrelay
  [[ $(stat -c '%a' /etc/anas-mail-relay/runtime.json) = 600 ]]
  if [[ ${ANAS_ACCESS_FORWARD:-0} = 1 ]]; then
    python3 - <<'PY'
import json
raw=open('/etc/anas-mail-relay/runtime.json','rb').read()
c=json.loads(raw)
assert c.get('forward_domain') == 'delivery.example.test'
assert b'r'*40 not in raw and b'd'*40 not in raw
print('forward domain persisted; provider tokens absent from VPS configuration')
PY
  fi
  echo 'isolated publish materials verified; systemd not activated'
fi
# A second sudo grant is rejected; removing it permits an idempotent retry.
printf 'anasmaildeploy ALL=(root) NOPASSWD: /usr/bin/id\n' > /etc/sudoers.d/anas-test-extra
chmod 0440 /etc/sudoers.d/anas-test-extra
if "$base/cloudrelay" bootstrap-install-access /etc/test-client.pub 127.0.0.1 localhost 127.0.0.1 "$port"; then echo 'extra sudo grant accepted' >&2; exit 1; fi
rm /etc/sudoers.d/anas-test-extra
"$base/cloudrelay" bootstrap-install-access /etc/test-client.pub 127.0.0.1 localhost 127.0.0.1 "$port"
# An extra group must not be silently accepted for the created identity.
/usr/sbin/groupadd anas-extra-test
/usr/sbin/usermod -a -G anas-extra-test anasmaildeploy
if "$base/cloudrelay" bootstrap-install-access /etc/test-client.pub 127.0.0.1 localhost 127.0.0.1 "$port"; then echo 'extra group accepted' >&2; exit 1; fi
/usr/sbin/usermod -G '' anasmaildeploy
/usr/sbin/groupdel anas-extra-test
if ! "$base/cloudrelay" bootstrap-install-access /etc/test-client.pub 127.0.0.1 localhost 127.0.0.1 "$port"; then
  echo 'isolated account recovery diagnostic:' >&2
  getent passwd anasmaildeploy >&2 || true
  getent group anasmaildeploy >&2 || true
  id -G anasmaildeploy >&2 || true
  LC_ALL=C sudo -n -ll -U anasmaildeploy >&2 || true
  exit 1
fi
# Removal of the managed key is never silently undone.
rm /var/lib/anas-mail-relay-bootstrap/.ssh/authorized_keys
if "$base/cloudrelay" bootstrap-install-access /etc/test-client.pub 127.0.0.1 localhost 127.0.0.1 "$port"; then echo 'missing key recreated' >&2; exit 1; fi
[[ ! -e /var/lib/anas-mail-relay-bootstrap/.ssh/authorized_keys ]]
echo 'excess sudo and extra group rejected, completed key deletion preserved'
