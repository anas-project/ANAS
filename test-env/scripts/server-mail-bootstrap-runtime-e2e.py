#!/usr/bin/env python3
"""Actual offline mail bootstrap and read-only company LDAP, isolated Docker only."""
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import time

source=Path(__file__).resolve().parents[2]
root=Path(os.environ['MAIL_BOOTSTRAP_TEST_ROOT'])
assert root.is_absolute() and root.name.startswith('anas-mail-bootstrap-test-') and not root.exists()
docker=os.environ['DOCKER_CMD']
subprocess.run(['bash','-c','source "$1"','mail-bootstrap-guard',str(source/'test-env/scripts/server-require-isolated-docker.sh')],check=True)
image='ghcr.io/anas-project/anas-samba-dc:4.23.6-r13'
expected='sha256:1d110032df763dd0ba7d1dbe233f4b4f40468322eb49f779677bbfa89a62333e'
name='anas_mail_bootstrap_'+secrets.token_hex(6)
root.mkdir(mode=0o700)
for part in ['input','evidence','data','candidate']:(root/part).mkdir(mode=0o700)


def run(cmd,log=None,timeout=90,check=True):
    result=subprocess.run(cmd,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=timeout)
    if log:(root/'evidence'/log).write_bytes(result.stdout)
    if check and result.returncode:raise RuntimeError('isolated mail bootstrap command failed: '+(log or cmd[0]))
    return result


def d(*args,**kwargs):return run([docker,*args],**kwargs)


failure=None;cleanup=[];created=False
try:
    assert d('image','inspect',image,'--format','{{.Id}}').stdout.decode().strip()==expected
    target=root/'candidate/modules/samba_dc/samba_dc/root'
    shutil.copytree(source/'modules/samba_dc/samba_dc/root',target)
    shutil.copy2(source/'modules/samba_dc/anchor_worker/mail_policy.py',target/'usr/local/share/anas/mail_policy.py')
    for script in ['mail-directory-schema-fixture.py','mail-bootstrap-fixture.py']:
        dest=root/'candidate/test-env/scripts'/script;dest.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(source/'test-env/scripts'/script,dest)
    binary=Path(os.environ['MAIL_COMPANY_TEST_BINARY']);assert binary.is_file()
    shutil.copy2(binary,root/'input/company.test');(root/'input/company.test').chmod(0o700)
    (root/'input/secrets.json').write_text(json.dumps({key:secrets.token_urlsafe(30)+'aA9!' for key in ['Administrator','alice','bob','reader','primary','portal','address','outsider']}))
    (root/'input/secrets.json').chmod(0o600)
    start='''#!/bin/bash
set -euo pipefail
python3 /candidate/test-env/scripts/mail-bootstrap-fixture.py provision
SAMBA_DC_BASE_DN=DC=mailschema,DC=invalid SAMBA_DC_DOMAIN_ACTION=provision bash /candidate/modules/samba_dc/samba_dc/root/usr/local/bin/install-identity-schema.sh
python3 /candidate/test-env/scripts/mail-bootstrap-fixture.py seed
python3 /candidate/test-env/scripts/mail-bootstrap-fixture.py audit-config
exec samba -i --no-process-group --option='tls enabled=yes' --option='tls cafile=/var/lib/samba/private/tls/ca.pem' --option='tls certfile=/var/lib/samba/private/tls/cert.pem' --option='tls keyfile=/var/lib/samba/private/tls/key.pem'
'''
    (root/'input/start.sh').write_text(start);(root/'input/start.sh').chmod(0o700)
    created=True
    d('run','-d','--name',name,'--label','anas.mail-bootstrap-test='+str(root),'--hostname','dc.mailschema.invalid','--network','bridge','--memory','768m','--cpus','2','--pids-limit','256','--entrypoint','/bin/bash',
      '-v',str(root/'candidate')+':/candidate:ro','-v',str(root/'input')+':/input:ro','-v',str(root/'evidence')+':/evidence','-v',str(root/'data')+':/var/lib/samba',image,'/input/start.sh')
    for _ in range(180):
        if d('exec',name,'ldbsearch','-H','ldap://127.0.0.1','-b','','-s','base','(objectClass=*)','defaultNamingContext',check=False).returncode==0:break
        if d('inspect','--format','{{.State.Running}}',name).stdout.decode().strip()!='true':raise RuntimeError('Samba bootstrap stopped before readiness')
        time.sleep(1)
    else:raise RuntimeError('Samba bootstrap readiness timeout')
    for phase in ['initial','changed']:
        if phase=='changed':d('exec',name,'python3','/candidate/test-env/scripts/mail-bootstrap-fixture.py','change',log='actual-change.log')
        d('exec','-e','MAIL_COMPANY_TEST_CONFIG=/evidence/company-live.json',name,'/input/company.test','-test.v','-test.run','^TestLiveIsolatedSambaCompanyDirectory$','-test.timeout','60s',log='company-'+phase+'.log')
    result={'passed':True,'image_id':expected,'identity_test_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'company_phases':['initial','changed']}
    (root/'evidence/results.json').write_text(json.dumps(result,indent=2));print(json.dumps(result),flush=True)
except BaseException as exc:
    failure=str(exc)
finally:
    if created:
        d('logs',name,log='container.log',check=False)
        if d('rm','-f','-v',name,log='container-remove.log',check=False).returncode:cleanup.append('test container removal failed')
    for path in [root/'input/secrets.json',root/'evidence/company-live.json']:
        try:path.unlink(missing_ok=True)
        except OSError:cleanup.append('private fixture credential cleanup failed')
    final={'failure':failure,'cleanup_errors':cleanup,'runtime_credentials_removed':not(root/'input/secrets.json').exists() and not(root/'evidence/company-live.json').exists()}
    (root/'evidence/final.json').write_text(json.dumps(final,indent=2));print(json.dumps(final),flush=True)
if failure or cleanup:raise SystemExit(1)
