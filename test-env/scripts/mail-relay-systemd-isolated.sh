#!/usr/bin/env bash
# TEST_CASES: MAILMOD-T-062 MAILMOD-T-064 MAILMOD-T-065
# Isolated Linux/systemd test; never use an existing service or source directory.
set -euo pipefail
bundle=$1
mode=${2:-standard}
id=$(python3 -c 'import secrets; print(secrets.token_hex(8))')
name="anas-mail-isolated-$id"
work="/run/$name"
binary_dir="/usr/local/libexec/$name"
unit="/run/systemd/system/$name.service"
state="/var/lib/$name"
private="/var/lib/private/$name"
for path in "$work" "$binary_dir" "$unit" "$state" "$private"; do
  if sudo -n test -e "$path" || sudo -n test -L "$path"; then echo 'isolated path collision' >&2; exit 1; fi
done
created=0
binary_created=0
cleanup() {
  result=$?
  if [ "$created" = 1 ]; then
    if [ "$result" != 0 ]; then sudo -n cat "$work/debug.log" 2>/dev/null || true; fi
    sudo -n systemctl stop "$name.service" >/dev/null 2>&1 || true
    active=$(sudo -n systemctl show --property=ActiveState --value "$name.service") || { echo 'cannot confirm unit stopped; preserving test state' >&2; return 1; }
    case "$active" in inactive|failed) ;; *) echo 'isolated unit still active; preserving test state' >&2; return 1 ;; esac
    if [ "$mode" = activate ]; then
      if ! sudo -n systemctl disable "$name.service" >/dev/null 2>&1; then echo 'cannot disable isolated unit; preserving test state' >&2; return 1; fi
      link="/etc/systemd/system/multi-user.target.wants/$name.service"
      if sudo -n test -e "$link" || sudo -n test -L "$link"; then echo 'isolated enable link remains; preserving test state' >&2; return 1; fi
    fi
    sudo -n rm -f -- "$unit"
    sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
    if sudo -n test -L "$state" && [ "$(sudo -n readlink "$state")" = "private/$name" ]; then sudo -n rm -f -- "$state"; fi
    if sudo -n test -d "$private" && ! sudo -n test -L "$private"; then sudo -n rm -rf -- "$private"; fi
    if [ "$binary_created" = 1 ]; then sudo -n rm -rf -- "$binary_dir"; fi
    sudo -n rm -rf -- "$work"
  fi
}
trap cleanup EXIT
sudo -n mkdir -m 0755 -- "$work"
created=1
sudo -n mkdir -m 0755 -- "$binary_dir"
binary_created=1
sudo -n install -m 0755 -- "$bundle/cloudrelay" "$binary_dir/cloudrelay"
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$bundle/key.pem" -out "$bundle/cert.pem" -days 1 -subj /CN=relay.example.test -addext 'subjectAltName=DNS:relay.example.test,IP:127.0.0.1' >/dev/null 2>&1
sudo -n install -m 0644 -- "$bundle/cert.pem" "$work/cert.pem"
sudo -n touch "$work/debug.log"
sudo -n chmod 0666 "$work/debug.log"
export bundle name work binary_dir
python3 - <<'PY'
import json,os,pathlib,secrets,socket,uuid
p=pathlib.Path(os.environ['bundle']); name=os.environ['name']
def port():
 s=socket.socket();s.bind(('127.0.0.1',0));v=s.getsockname()[1];s.close();return v
https=port();smtp=port()
while smtp==https: smtp=port()
c={'schema':'anas.mail-vps-runtime/v1','directory':f'/var/lib/{name}/data/source','source_id':str(uuid.uuid4()),'generation':str(uuid.uuid4()),'domains':['example.test'],'capacity_bytes':8388608,'delivered_days':7,'listen':f'127.0.0.1:{https}','certificate_host':'127.0.0.1','reader':secrets.token_hex(32),'control':secrets.token_hex(32),'tls_certificate':(p/'cert.pem').read_text(),'tls_private_key':(p/'key.pem').read_text(),'notification_origin':'','notification_secret':'','notification_ca':'','smtp_listen':f'127.0.0.1:{smtp}','smtp_hostname':'relay.example.test'}
(p/'config.json').write_text(json.dumps(c));os.chmod(p/'config.json',0o600)
unit=(p/'anas-mail-relay.service').read_text().replace('anas-mail-relay',name).replace('/etc/'+name+'/runtime.json',os.environ['work']+'/config.json')
unit=unit.replace('UMask=0077','UMask=0077\nEnvironment=SSL_CERT_FILE='+os.environ['work']+'/cert.pem\nStandardOutput=append:'+os.environ['work']+'/debug.log\nStandardError=append:'+os.environ['work']+'/debug.log')
(p/'test.service').write_text(unit)
PY
sudo -n install -m 0600 -- "$bundle/config.json" "$work/config.json"
sudo -n install -m 0644 -- "$bundle/test.service" "$unit"
sudo -n systemctl daemon-reload
if [ "$mode" = activate ]; then
  sudo -n install -m 0755 -- "$bundle/inspect.test" "$binary_dir/inspect.test"
  sudo -n env ANAS_MAIL_TEST_ACTIVATE_NAME="$name.service" ANAS_MAIL_TEST_ACTIVATE_WORK="$work" ANAS_MAIL_TEST_ACTIVATE_BINARY="$binary_dir/cloudrelay" "$binary_dir/inspect.test" -test.run '^TestActivateSystemdLiveUnit$' -test.v
else
  if ! sudo -n systemctl start "$name.service"; then
    sudo -n cat "$work/debug.log" || true
    exit 1
  fi
fi
python3 - <<'PY'
import hashlib,json,os,pathlib,smtplib,ssl,time,urllib.error,urllib.parse,urllib.request,uuid
p=pathlib.Path(os.environ['bundle']);c=json.loads((p/'config.json').read_text());ctx=ssl.create_default_context(cafile=str(p/'cert.pem'));url='https://'+c['listen']
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ctx))
def req(path,token,data=None):
 h={'Authorization':'Bearer '+token}
 if data is not None:h['Content-Type']='application/json'
 return opener.open(urllib.request.Request(url+path,data=data,headers=h),timeout=5).read()
for i in range(80):
 try: assert json.loads(req('/v1/status',c['reader']))['source_id']==c['source_id'];break
 except Exception:
  if i==79:raise
  time.sleep(.1)
try: req('/v1/status','0'*64);raise AssertionError('unauthorized')
except urllib.error.HTTPError as e:assert e.code==401
owner,box=str(uuid.uuid4()),str(uuid.uuid4());routes=[{'address':a,'mailbox_id':box,'owner_anchor':owner,'claim_version':str(uuid.uuid4())} for a in ['alias@example.test','person@example.test']]
fingerprint=hashlib.sha256(json.dumps(routes,separators=(',',':')).encode()).hexdigest();body={'source_id':c['source_id'],'generation':c['generation'],'expected_revision':0,'operation_id':str(uuid.uuid4()),'fingerprint':fingerprint,'routes':routes}
assert json.loads(req('/v1/routes',c['control'],json.dumps(body,separators=(',',':')).encode()))['revision']==1
smtp=smtplib.SMTP('127.0.0.1',int(c['smtp_listen'].rsplit(':',1)[1]),timeout=5);smtp.ehlo();smtp.starttls(context=ctx);smtp.ehlo();message=b'From: sender@example.net\r\nTo: alias@example.test\r\nSubject: isolated systemd test\r\n\r\ntest\r\n';smtp.sendmail('sender@example.net',['alias@example.test'],message);smtp.quit()
page=json.loads(req('/v1/messages',c['reader']));assert len(page['messages'])==1
meta=page['messages'][0]['metadata'];assert meta['accepted_address']=='alias@example.test' and meta['mailbox_id']==box
path='/v1/messages/'+urllib.parse.quote(meta['message_id'])+'/raw?target='+urllib.parse.quote(box);assert req(path,c['reader'])==message
(p/'retrieval.json').write_text(json.dumps({'path':path}));print('isolated systemd TLS, route, SMTP alias and source read: PASS')
PY
sudo -n systemctl restart "$name.service"
python3 - <<'PY'
import json,os,pathlib,ssl,time,urllib.request
p=pathlib.Path(os.environ['bundle']);c=json.loads((p/'config.json').read_text());path=json.loads((p/'retrieval.json').read_text())['path'];ctx=ssl.create_default_context(cafile=str(p/'cert.pem'));opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ctx));url='https://'+c['listen']+path
for i in range(80):
 try:
  r=opener.open(urllib.request.Request(url,headers={'Authorization':'Bearer '+c['reader']}),timeout=5).read();assert b'isolated systemd test' in r;break
 except Exception:
  if i==79:raise
  time.sleep(.1)
print('isolated systemd restart preserves original: PASS')
PY
if [ -f "$bundle/inspect.test" ]; then
  sudo -n install -m 0755 -- "$bundle/inspect.test" "$binary_dir/inspect.test"
  pid=$(sudo -n systemctl show --property=MainPID --value "$name.service")
  sudo -n env ANAS_MAIL_TEST_UNIT_NAME="$name.service" ANAS_MAIL_TEST_UNIT_PID="$pid" ANAS_MAIL_TEST_UNIT_BINARY="$binary_dir/cloudrelay" ANAS_MAIL_TEST_UNIT_CONFIG="$work/config.json" ANAS_MAIL_TEST_UNIT_CA="$work/cert.pem" "$binary_dir/inspect.test" -test.run '^TestSystemdInspectionLiveUnit$' -test.v
fi
