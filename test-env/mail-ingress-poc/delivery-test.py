#!/usr/bin/env python3
"""Real Dovecot LDA checks; only the approved finance fixture, never production."""
import email
import email.policy
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time
import uuid

ROOT = Path('/home/whl/anas-mail-ingress-test-20261025')
os.environ['DOCKER_HOST'] = 'unix:///run/anas-mail-test-1024.sock'
assert subprocess.check_output(['docker','info','--format','{{.DockerRootDir}}'],text=True).strip() == '/home/whl/anas-mail-go-test-20261024/docker'
inventory = json.loads(subprocess.check_output(['python3',str(ROOT/'consumer_server.py'),'inventory']))
assert len(inventory)==2 and all(c['running'] for c in inventory)
base=['docker','compose','-f',str(ROOT/'compose.json'),'exec','-T','mail']
report={'started':time.time(),'checks':[]}
out=ROOT/'evidence-scheduler';out.mkdir(mode=0o700,exist_ok=True)
user='box-a@ingress.invalid'
conf='/etc/dovecot/conf.d/99-ingress-poc.conf'
script='/var/mail/ingress.invalid/box-a/home/.ingress-poc.sieve'

def command(*args,data=None,check=True):
    return subprocess.run(base+list(args),input=data,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=check,timeout=30)
def fetch(marker):
    return json.loads(command('doveadm','-f','json','fetch','-u',user,'guid text mailbox','HEADER','X-ANAS-Test-ID',marker).stdout)
def send(subject,extra=(),target=user):
    marker=str(uuid.uuid4())
    raw=(f'From: sender@example.net\r\nTo: forged@example.net\r\nX-ANAS-Test-ID: {marker}\r\nSubject: {subject}\r\n'
         'MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=lda\r\n\r\n'
         '--lda\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n正常投递验证\r\n'
         '--lda\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\nAAECA/8=\r\n--lda--\r\n').encode()
    p=command('/usr/lib/dovecot/dovecot-lda',*extra,'-e','-d',target,'-f','sender@example.net','-a',target,data=raw,check=False)
    return marker,raw,p
def check(name,fn):
    started=time.time()
    try:
        details=fn();report['checks'].append({'name':name,'passed':True,'seconds':time.time()-started,'details':details})
    except Exception as e:
        report['checks'].append({'name':name,'passed':False,'error':str(e)});raise
    finally:
        (out/'delivery-results.json').write_text(json.dumps(report,ensure_ascii=False,indent=2));(out/'delivery-results.json').chmod(0o600)
def content(raw,stored):
    def parts(x):
        msg=email.message_from_bytes(x,policy=email.policy.default)
        return [(p.get_content_type(),p.get_payload(decode=True).replace(b'\r\n',b'\n')) for p in msg.walk() if not p.is_multipart()]
    assert parts(raw)==parts(stored.encode()),'MIME payload changed'
def delivery(subject,folder):
    marker,raw,p=send(subject);assert p.returncode==0,p.stderr.decode()
    rows=fetch(marker);assert len(rows)==1 and rows[0]['mailbox']==folder,rows
    content(raw,rows[0]['text'])
    return {'mailbox':folder,'mime_payload_equal':True,'guid':rows[0]['guid'],'raw_sha256':hashlib.sha256(raw).hexdigest()}

command('python3','-c',"from pathlib import Path; assert not Path("+repr(conf)+").exists() and not Path("+repr(script)+").exists()")
try:
    code="""from pathlib import Path
import os
conf=Path(%r);script=Path(%r)
assert not conf.exists() and not script.exists()
conf.write_text('sieve_script ingress_poc {\\n  type = personal\\n  path = '+str(script)+'\\n}\\n')
script.write_text('require ["fileinto"]; if header :is "Subject" "route-filter" { fileinto "Filtered"; stop; } if header :is "Subject" "discard-filter" { discard; stop; }')
os.chown(script,5000,5000);script.chmod(0o600)
""" % (conf,script)
    command('python3','-c',code)
    command('doveconf','-n');command('dovecot','reload')
    check('LDA keeps MIME body and attachment',lambda:delivery('normal','INBOX'))
    check('LDA executes user Sieve fileinto',lambda:delivery('route-filter','Filtered'))
    def discard():
        marker,raw,p=send('discard-filter');assert p.returncode==0,p.stderr.decode();assert fetch(marker)==[]
        return {'lda_exit':0,'stored_copies':0,'meaning':'successful Sieve discard is not proof of inbox storage'}
    check('Sieve discard exposes acknowledgement boundary',discard)
    def quota():
        marker,raw,p=send('quota-failure',('-o','quota_storage_size=1k','-o','quota_storage_grace=0'))
        assert p.returncode!=0,'over-quota delivery was accepted'
        assert b'quota' in p.stderr.lower(),p.stderr.decode()
        assert fetch(marker)==[]
        return {'lda_exit':p.returncode,'error':p.stderr.decode().strip(),'stored_copies':0}
    check('quota failure is not accepted or stored',quota)
    def unknown():
        marker,raw,p=send('unknown-user',target='absent@ingress.invalid');assert p.returncode!=0
        assert fetch(marker)==[]
        return {'lda_exit':p.returncode,'stored_copies':0}
    check('unknown mailbox is rejected',unknown)
    report['passed']=True
finally:
    command('python3','-c',"from pathlib import Path\nfor name in "+repr([conf,script,str(Path(script).with_suffix('.svbin'))])+":\n Path(name).unlink(missing_ok=True)",check=False)
    command('dovecot','reload')
    command('python3','-c',"from pathlib import Path; assert not any(Path(x).exists() for x in "+repr([conf,script,str(Path(script).with_suffix('.svbin'))])+")")
    report['cleanup_restored']=True
    report['finished']=time.time();report['source_sha256']=hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    (out/'delivery-results.json').write_text(json.dumps(report,ensure_ascii=False,indent=2));(out/'delivery-results.json').chmod(0o600)
    print(json.dumps(report,ensure_ascii=False))
