#!/usr/bin/env bash
# TEST_CASES: MAILMOD-T-062, MAILMOD-T-063, MAILMOD-T-064, MAILMOD-T-065
# Explicitly invoked on a test host. Creates a private Incus project with no
# profiles or network devices and a pinned public Ubuntu image. It never binds
# host paths into the instance or touches the host's systemd service.
set -euo pipefail
[[ $# = 1 && -f $1 && -x $1 ]] || exit 2
binary=$1
fingerprint=a8fa6db1a9fa3faa9ff4cd7958b0c758d206088e4d13d8eceda52bbfc53730fe
suffix=$(python3 -c 'import secrets; print(secrets.token_hex(8))')
project="anas-mail-systemd-test-$suffix"
instance="relay-$suffix"
work=$(mktemp -d /tmp/anas-incus-mail.XXXXXXXX)
created=0
cleanup() {
  rc=$?
  if [[ $created = 1 ]]; then
    marker=$(sudo -n incus project get "$project" user.anas_mail_test 2>/dev/null || true)
    if [[ $marker != "$suffix" ]]; then echo 'project ownership unknown; preserving' >&2; return 1; fi
    if sudo -n incus --project "$project" info "$instance" >/dev/null 2>&1; then
      identity=$(sudo -n incus --project "$project" config get "$instance" user.anas_mail_test || true)
      if [[ $identity != "$suffix" ]]; then echo 'instance ownership unknown; preserving' >&2; return 1; fi
      if [[ $rc != 0 ]]; then
        sudo -n incus --project "$project" exec "$instance" -- systemctl status anas-mail-relay.service --no-pager -n 15 2>/dev/null || true
        if [[ ${ANAS_INCUS_HOLD_FAILURE:-0} = 1 ]]; then echo 'holding isolated failure for inspection'; sleep 45; fi
      fi
      sudo -n incus --project "$project" delete "$instance" --force || { echo 'instance cleanup failed' >&2; return 1; }
    fi
    if sudo -n incus --project "$project" image info "$fingerprint" >/dev/null 2>&1; then
      sudo -n incus --project "$project" image delete "$fingerprint" || { echo 'test image cleanup failed' >&2; return 1; }
    fi
    sudo -n incus project delete "$project" || { echo 'project cleanup failed' >&2; return 1; }
  fi
  rm -rf -- "$work"
  return "$rc"
}
trap cleanup EXIT
if sudo -n incus project show "$project" >/dev/null 2>&1; then echo 'project name collision' >&2; exit 1; fi
sudo -n incus project create "$project" -c user.anas_mail_test="$suffix" </dev/null >/dev/null
created=1
printf 'isolated project %s, image %s\n' "$project" "$fingerprint"
sudo -n incus --project "$project" init "images:$fingerprint" "$instance" --no-profiles --storage anas-btrfs -c user.anas_mail_test="$suffix" -c limits.memory=768MiB -c limits.cpu=2 </dev/null
sudo -n incus --project "$project" config device set "$instance" root size 2GiB
[[ $(sudo -n incus --project "$project" config device list "$instance") = root ]]
[[ $(sudo -n incus --project "$project" config device get "$instance" root type) = disk ]]
[[ $(sudo -n incus --project "$project" config device get "$instance" root path) = / ]]
[[ $(sudo -n incus --project "$project" config device get "$instance" root pool) = anas-btrfs ]]
[[ $(sudo -n incus --project "$project" config get "$instance" security.privileged) != true ]]
[[ -z $(sudo -n incus --project "$project" config get "$instance" raw.lxc) ]]
sudo -n incus --project "$project" start "$instance"
for _ in {1..80}; do
  if sudo -n incus --project "$project" exec "$instance" -- systemctl is-system-running 2>/dev/null | grep -Eq 'running|degraded'; then break; fi
  sleep .5
done
sudo -n incus --project "$project" exec "$instance" -- systemctl show --property=SystemState --value
sudo -n incus --project "$project" exec "$instance" -- sh -c 'test "$(ls /sys/class/net)" = lo && test "$(cat /proc/1/comm)" = systemd && command -v update-ca-certificates && command -v python3'
if [[ ${ANAS_INCUS_DISCOVER:-0} = 1 ]]; then
  sudo -n incus --project "$project" exec "$instance" -- sh -c 'for c in sshd sudo ssh-keygen openssl systemctl; do if command -v "$c" >/dev/null; then echo "$c available"; else echo "$c unavailable"; fi; done'
  printf 'DISCOVERY_READY %s %s %s\n' "$project" "$instance" "$work"
  IFS= read -r release
  [[ $release = finished ]]
  exit 0
fi
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$work/key.pem" -out "$work/cert.pem" -days 1 -subj /CN=anas-isolated-test -addext 'subjectAltName=IP:127.0.0.1,DNS:relay.example.test' -addext 'basicConstraints=critical,CA:TRUE' >/dev/null 2>&1
export ANAS_INCUS_TEST_WORK="$work"
python3 - <<'PY'
import json,os,pathlib,secrets,uuid
p=pathlib.Path(os.environ['ANAS_INCUS_TEST_WORK'])
c={'schema':'anas.mail-vps-runtime/v1','directory':'/var/lib/anas-mail-relay/data/source','source_id':str(uuid.uuid4()),'generation':str(uuid.uuid4()),'domains':['example.test'],'capacity_bytes':8388608,'delivered_days':7,'listen':'0.0.0.0:18448','certificate_host':'127.0.0.1','reader':secrets.token_hex(32),'control':secrets.token_hex(32),'tls_certificate':(p/'cert.pem').read_text(),'tls_private_key':(p/'key.pem').read_text(),'notification_origin':'','notification_secret':'','notification_ca':'','smtp_listen':'0.0.0.0:25','smtp_hostname':'relay.example.test'}
(p/'runtime.json').write_text(json.dumps(c,separators=(',',':')));os.chmod(p/'runtime.json',0o600)
PY
sudo -n incus --project "$project" file push "$binary" "$instance/tmp/cloudrelay" --uid 0 --gid 0 --mode 0755
sudo -n incus --project "$project" file push "$work/runtime.json" "$instance/tmp/runtime.json" --uid 0 --gid 0 --mode 0600
sudo -n incus --project "$project" file push "$work/cert.pem" "$instance/usr/local/share/ca-certificates/anas-isolated-test.crt" --uid 0 --gid 0 --mode 0644
sudo -n incus --project "$project" exec "$instance" -- update-ca-certificates >/dev/null
sudo -n incus --project "$project" exec "$instance" -- stat -c 'private input mode=%a uid=%u gid=%g links=%h' /tmp/runtime.json
sudo -n incus --project "$project" exec "$instance" -- sh -c 'for p in /etc /usr/local/libexec /etc/systemd/system /var/lib /run/systemd/system /run/systemd/transient /usr/lib/systemd/system /lib/systemd/system; do if test -d "$p"; then stat -c "%n %a %U %G" "$p"; else printf "%s absent\n" "$p"; fi; done'
sudo -n incus --project "$project" exec "$instance" -- /tmp/cloudrelay preflight /tmp/runtime.json
sudo -n incus --project "$project" exec "$instance" -- /tmp/cloudrelay publish-systemd /tmp/runtime.json
# The original image's override must be rejected before any enable/start.
set +e
override_result=$(sudo -n incus --project "$project" exec "$instance" -- /tmp/cloudrelay activate-systemd /tmp/runtime.json)
override_status=$?
set -e
[[ $override_status != 0 && $override_result = '{"state":"conflict"}' ]]
sudo -n incus --project "$project" exec "$instance" -- sh -c 'test "$(systemctl is-enabled anas-mail-relay.service)" = disabled && ! test -e /etc/anas-mail-relay/start.authorized'
echo 'unsafe global systemd override refused before activation'
# Incus injects a global LXC compatibility drop-in that clears LoadCredential
# and weakens other hardening. It is regenerated on daemon-reload. The test
# disables only the pinned image's exact known generator and its exact generated
# file inside this disposable container, never an unknown host policy.
sudo -n incus --project "$project" exec "$instance" -- python3 - <<'PY'
import hashlib,os
from pathlib import Path
g=Path('/etc/systemd/system-generators/lxc')
if g.is_symlink() or not g.is_file() or hashlib.sha256(g.read_bytes()).hexdigest()!='89545cf279d9347b3e0ca44a263a457f12da145b0d5bbbdc00d5b8cdbde70b93':raise SystemExit('unknown LXC generator; refuse')
os.chmod(g,0o644)
p=Path('/run/systemd/system/service.d/zzz-lxc-service.conf')
expected='[Service]\nProcSubset=all\nProtectProc=default\nProtectControlGroups=no\nProtectKernelTunables=no\nNoNewPrivileges=no\nLoadCredential=\nPrivateNetwork=no\n'
if p.exists():
 if p.is_symlink() or p.read_text()!=expected:raise SystemExit('unknown systemd override; refuse')
 p.unlink()
PY
sudo -n incus --project "$project" exec "$instance" -- systemctl daemon-reload
sudo -n incus --project "$project" exec "$instance" -- systemctl show anas-mail-relay.service --property=Id,LoadState,FragmentPath,DropInPaths,NeedDaemonReload,Transient,DynamicUser,ActiveState,SubState,UnitFileState,MainPID --no-pager
sudo -n incus --project "$project" exec "$instance" -- /tmp/cloudrelay activate-systemd /tmp/runtime.json
sudo -n incus --project "$project" exec "$instance" -- /tmp/cloudrelay activate-systemd /tmp/runtime.json
sudo -n incus --project "$project" exec "$instance" -- /tmp/cloudrelay inspect-systemd /tmp/runtime.json
sudo -n incus --project "$project" exec "$instance" -- python3 - <<'PY'
import hashlib,json,smtplib,ssl,time,urllib.error,urllib.parse,urllib.request,uuid
c=json.load(open('/tmp/runtime.json'));context=ssl.create_default_context();url='https://127.0.0.1:18448'
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=context))
def req(path,token,data=None):
 h={'Authorization':'Bearer '+token}
 if data is not None:h['Content-Type']='application/json'
 return opener.open(urllib.request.Request(url+path,data=data,headers=h),timeout=5).read()
assert json.loads(req('/v1/status',c['reader']))['source_id']==c['source_id']
try:req('/v1/status','0'*64);raise AssertionError('wrong reader accepted')
except urllib.error.HTTPError as e:assert e.code==401
box,extra,owner=str(uuid.uuid4()),str(uuid.uuid4()),str(uuid.uuid4())
targets={'person@example.test':box,'alias@example.test':box,'extra@example.test':extra}
routes=[{'address':a,'mailbox_id':b,'owner_anchor':owner,'claim_version':str(uuid.uuid4())} for a,b in sorted(targets.items())]
body={'source_id':c['source_id'],'generation':c['generation'],'expected_revision':0,'operation_id':str(uuid.uuid4()),'fingerprint':hashlib.sha256(json.dumps(routes,separators=(',',':')).encode()).hexdigest(),'routes':routes}
assert json.loads(req('/v1/routes',c['control'],json.dumps(body,separators=(',',':')).encode()))['revision']==1
smtp=smtplib.SMTP('127.0.0.1',25,timeout=5);smtp.ehlo();smtp.starttls(context=context);smtp.ehlo()
messages={}
for address in targets:
 message=('From: sender@example.net\r\nTo: '+address+'\r\nSubject: isolated activation\r\n\r\nsynthetic test\r\n').encode()
 smtp.sendmail('sender@example.net',[address],message);messages[address]=message
try:smtp.sendmail('sender@example.net',['unknown@example.test'],b'From: sender@example.net\r\n\r\nunknown\r\n');raise AssertionError('unknown recipient accepted')
except smtplib.SMTPRecipientsRefused:pass
smtp.quit()
page=json.loads(req('/v1/messages',c['reader']));assert len(page['messages'])==3
paths={}
for item in page['messages']:
 meta=item['metadata'];address=meta['accepted_address'];assert address in targets and meta['mailbox_id']==targets[address]
 path='/v1/messages/'+urllib.parse.quote(meta['message_id'])+'/raw?target='+urllib.parse.quote(meta['mailbox_id'])
 assert req(path,c['reader'])==messages[address];paths[address]=path
assert set(paths)==set(targets)
open('/tmp/retrieval-path','w').write(json.dumps(paths))
print('actual fixed unit: TLS, primary/alias/extra routing, SMTP and raw read passed')
PY
sudo -n incus --project "$project" exec "$instance" -- systemctl restart anas-mail-relay.service
sudo -n incus --project "$project" exec "$instance" -- python3 - <<'PY'
import json,ssl,time,urllib.request
c=json.load(open('/tmp/runtime.json'));paths=json.load(open('/tmp/retrieval-path'));ctx=ssl.create_default_context();opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ctx))
for address,path in paths.items():
 for i in range(80):
  try:
   r=opener.open(urllib.request.Request('https://127.0.0.1:18448'+path,headers={'Authorization':'Bearer '+c['reader']}),timeout=3).read();assert b'isolated activation' in r and address.encode() in r;break
  except Exception:
   if i==79:raise
   time.sleep(.1)
print('actual fixed unit restart preserves all originals')
PY
sudo -n incus --project "$project" exec "$instance" -- /tmp/cloudrelay inspect-systemd /tmp/runtime.json
echo 'isolated fixed systemd activation and restart verified'
