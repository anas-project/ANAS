#!/usr/bin/env python3
"""Real scoped Runner API gate in an exact disposable QEMU VM, without Docker.

Runs a new loopback-only Forgejo/SQLite fixture as an ordinary user. It verifies
the production client's API compatibility, not a real job or database matrix.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import sys
import time
import urllib.request
import urllib.error

BASE = 'http://127.0.0.1:13000'


def generated_password(output):
    matches=re.findall(r"(?im)^generated random password is '([^'\r\n]{48})'\s*$",output)
    if len(matches)!=1:
        raise RuntimeError('random fixture credential could not be parsed safely')
    return matches[0]


def require_vm(identity):
    if (os.geteuid() == 0 or not re.fullmatch(r'anas-runner-bake-[a-z0-9]{6}', identity)
            or Path('/var/lib/cloud/data/instance-id').read_text().strip() != identity
            or Path('/sys/class/dmi/id/sys_vendor').read_text().strip() != 'QEMU'
            or Path('/var/run/docker.sock').exists() or Path('/var/lib/docker').exists()):
        raise RuntimeError('exact disposable QEMU VM without Docker and non-root owner required')


def run(args):
    require_vm(args.vm_id)
    root = Path(args.report_root)
    if not root.is_absolute() or root.exists():
        raise RuntimeError('new absolute fixture/report directory required')
    for value in (args.forgejo, args.tests, args.test2json):
        p = Path(value)
        if not p.is_absolute() or p.is_symlink() or not p.is_file():
            raise RuntimeError('explicit regular executable inputs required')
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 13000))
    os.umask(0o077)
    root.mkdir(mode=0o700)
    env = {'PATH':'/usr/local/bin:/usr/bin:/bin','HOME':str(root),'USER':'anas-test','GOMAXPROCS':'1'}

    def cli(*arguments):
        result = subprocess.run([args.forgejo,*arguments,'--config',str(root/'app.ini'),'--work-path',str(root)],
                                env=env,stdin=subprocess.DEVNULL,capture_output=True,timeout=90)
        if result.returncode or len(result.stdout)+len(result.stderr)>4<<20:
            raise RuntimeError('fixed Forgejo fixture CLI failed')
        return result.stdout.decode()

    (root/'app.ini').write_text(f'''APP_NAME = ANAS disposable API gate
RUN_USER = anas-test
RUN_MODE = prod
[database]
DB_TYPE = sqlite3
PATH = {root}/forgejo.db
[repository]
ROOT = {root}/repositories
[server]
PROTOCOL = http
HTTP_ADDR = 127.0.0.1
HTTP_PORT = 13000
ROOT_URL = {BASE}/
DISABLE_SSH = true
OFFLINE_MODE = true
APP_DATA_PATH = {root}/data
[security]
INSTALL_LOCK = true
SECRET_KEY = {secrets.token_hex(32)}
INTERNAL_TOKEN = {secrets.token_hex(64)}
[service]
DISABLE_REGISTRATION = true
[mailer]
ENABLED = false
[actions]
ENABLED = true
[cron]
ENABLED = false
[log]
MODE = console
LEVEL = Error
''')
    cli('migrate')
    creation = cli('admin','user','create','--username','anas-api-lab','--email','anas-api-lab@example.invalid',
                   '--admin','--random-password','--random-password-length','48','--must-change-password=false')
    try:
        password = generated_password(creation)
    except RuntimeError:
        (root/'app.ini').unlink(missing_ok=True)
        raise
    creation = ''
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    auth = base64.b64encode(('anas-api-lab:'+password).encode()).decode()

    def api(method, path, value=None):
        body = None if value is None else json.dumps(value).encode()
        request = urllib.request.Request(BASE+path,data=body,method=method,
            headers={'Authorization':'Basic '+auth,'Content-Type':'application/json'})
        try:
            with opener.open(request,timeout=10) as response:
                body=response.read((4<<20)+1)
                if len(body)>4<<20:raise RuntimeError('oversized fixture API response')
                return json.loads(body) if body else None
        except urllib.error.HTTPError as error:
            raise RuntimeError('fixture API returned status '+str(error.code)) from None

    server = None
    try:
        with (root/'server-error.log').open('xb') as log:
            server = subprocess.Popen([args.forgejo,'web','--config',str(root/'app.ini'),'--work-path',str(root)],
                                      env=env,stdin=subprocess.DEVNULL,stdout=log,stderr=log)
        deadline=time.monotonic()+60
        while True:
            if server.poll() is not None:raise RuntimeError('fixture server exited before readiness')
            try:
                version=api('GET','/api/v1/version')
                if version.get('version','').split('+',1)[0]!='15.0.7':raise RuntimeError('unexpected Forgejo fixture version')
                break
            except (OSError,urllib.error.URLError):
                if time.monotonic()>deadline:raise RuntimeError('fixture server readiness timeout') from None
                time.sleep(.2)
        api('POST','/api/v1/user/repos',{'name':'repo','private':True,'auto_init':False})
        api('POST','/api/v1/orgs',{'username':'anas-api-org','visibility':'private'})
        fixture=root/'native.json'
        fixture.write_text(json.dumps({'vm_id':args.vm_id,'base_url':BASE,'username':'anas-api-lab','password':password}))
        try:
            with (root/'native.jsonl').open('x') as output:
                result=subprocess.run([args.test2json,'-t','-p','github.com/anas-project/ANAS/modules/forgejo/actions-controller',
                    args.tests,'-test.v=test2json','-test.run=^TestNativeForgejoScopedRunnerAPI$','-test.count=1','-test.timeout=90s'],
                    env={**env,'ANAS_REQUIRE_FORGEJO_API_NATIVE':'1','ANAS_FORGEJO_API_FIXTURE':str(fixture)},
                    stdin=subprocess.DEVNULL,stdout=output,stderr=subprocess.STDOUT,timeout=100)
        finally:
            fixture.unlink(missing_ok=True)
        events=[json.loads(line) for line in (root/'native.jsonl').read_text().splitlines()]
        # Independent readback and exact cleanup, even if client receipt parsing failed.
        for path in ('/api/v1/repos/anas-api-lab/repo/actions/runners','/api/v1/orgs/anas-api-org/actions/runners'):
            rows=api('GET',path)
            if isinstance(rows,dict):rows=rows.get('runners')
            if not isinstance(rows,list):raise RuntimeError('runner inventory is not a confirmed list')
            for runner in rows:
                if runner.get('name')!='anas-api-fixture':raise RuntimeError('unknown fixture runner; cleanup refused')
                api('DELETE',path+'/'+str(runner['id']))
            rows=api('GET',path)
            if isinstance(rows,dict):rows=rows.get('runners')
            if rows!=[]:raise RuntimeError('fixture runner cleanup unconfirmed')
        required={'TestNativeForgejoScopedRunnerAPI','TestNativeForgejoScopedRunnerAPI/anas-api-lab-repo','TestNativeForgejoScopedRunnerAPI/anas-api-org'}
        passed={e.get('Test') for e in events if e.get('Action')=='pass'}
        success=result.returncode==0 and required.issubset(passed) and not any(e.get('Action') in ('fail','skip') for e in events) and any(e.get('Action')=='pass' and not e.get('Test') for e in events)
        summary={'real_scoped_runner_api':success,'forgejo_version':'15.0.7','required_passes':len(required),'registrations_absent':True,'real_job_executed':False}
        (root/'summary.json').write_text(json.dumps(summary))
        print(json.dumps(summary),flush=True)
        if not success:raise RuntimeError('real scoped Runner API test failed; private JSONL retained')
    finally:
        if server is not None:
            if server.poll() is None:server.terminate()
            try:server.wait(timeout=15)
            except subprocess.TimeoutExpired:
                server.kill();server.wait(timeout=5)
        (root/'native.json').unlink(missing_ok=True)
        (root/'app.ini').unlink(missing_ok=True)


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    for name in ('vm-id','forgejo','tests','test2json','report-root'):parser.add_argument('--'+name,required=True)
    try:run(parser.parse_args())
    except Exception as error:
        print(json.dumps({'passed':False,'error_type':type(error).__name__,'message':str(error) if type(error) is RuntimeError else 'private API fixture failed'}),file=sys.stderr)
        sys.exit(1)
