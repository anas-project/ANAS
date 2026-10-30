#!/usr/bin/env python3
"""Dedicated two-container fixture. Never use the default Docker daemon."""
import json
import hashlib
import os
from pathlib import Path
import secrets
import subprocess
import sys
import time

ROOT = Path('/home/whl/anas-mail-ingress-test-20261025')
CACHE = Path('/home/whl/anas-mail-go-test-20261024')
PROJECT = 'anas_mail_ingress_1025'
SOCKET = 'unix:///run/anas-mail-test-1024.sock'
os.environ['DOCKER_HOST'] = SOCKET
def run(args, data=None):
    return subprocess.check_output(args, input=data, stderr=subprocess.PIPE).decode()
def compose(*args): return run(['docker', 'compose', '-f', str(ROOT/'compose.json'), *args])
def execute(service, *args, data=None):
    return run(['docker', 'compose', '-f', str(ROOT/'compose.json'), 'exec', '-T', service, *args], data)
def psql(*args):
    return execute('postgres','sh','-c','export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql "$@"',
        'psql','-h','127.0.0.1','-U','ingress','-d','ingress','-v','ON_ERROR_STOP=1',*args)
def write(path, data):
    p = ROOT/path
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(data); p.chmod(0o600)
def guard():
    assert Path(run(['docker','info','--format','{{.DockerRootDir}}']).strip()) == CACHE/'docker'
    if not (ROOT/'compose.json').exists(): return []
    cfg=json.loads((ROOT/'compose.json').read_text()); assert cfg['name']==PROJECT
    ids=compose('ps','-aq').split()
    rows=json.loads(run(['docker','inspect',*ids])) if ids else []
    for c in rows:
        labels=c['Config']['Labels']
        assert labels['com.docker.compose.project']==PROJECT
        assert labels['com.docker.compose.project.working_dir']==str(ROOT)
        assert labels['anas.mail-ingress-poc']=='true'
        for m in c['Mounts']:
            assert m['Type']!='volume'
            if m['Type']=='bind': assert Path(m['Source']).resolve().is_relative_to(ROOT)
    return rows
guard()
mode=sys.argv[1]
if mode=='prepare':
    assert not (ROOT/'compose.json').exists()
    old=json.loads((CACHE/'mail-test-recovery/compose.json').read_text())
    assert all(x['image'].startswith('sha256:') for x in [old['services']['mail'],old['services']['postgres']])
    ROOT.mkdir(mode=0o700,exist_ok=True)
    for d in ['data/mail','data/state','data/logs','data/config','data/tls','data/postgres','data/spool','evidence']:
        (ROOT/d).mkdir(parents=True,exist_ok=True)
    password=secrets.token_urlsafe(32)
    write(Path('data/config/postfix-accounts.cf'), ''.join(f'box-{x}@ingress.invalid|{{PLAIN}}{password}\n' for x in ['a','b']))
    run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','3','-subj','/CN=mail.ingress.invalid',
        '-addext','subjectAltName=DNS:mail.ingress.invalid','-keyout',str(ROOT/'data/tls/key.pem'),'-out',str(ROOT/'data/tls/cert.pem')])
    (ROOT/'data/tls/key.pem').chmod(0o600)
    common={'restart':'no','labels':{'anas.mail-ingress-poc':'true'},'networks':['ingress']}
    pgpass=secrets.token_urlsafe(32)
    cfg={'name':PROJECT,'services':{
        'mail':{**common,'image':old['services']['mail']['image'],'hostname':'mail.ingress.invalid','mem_limit':'512m',
            'environment':{'ENABLE_CLAMAV':'0','ENABLE_AMAVIS':'0','ENABLE_SPAMASSASSIN':'0','ENABLE_FAIL2BAN':'0',
                'ENABLE_RSPAMD':'0','ENABLE_OPENDKIM':'0','ENABLE_OPENDMARC':'0','ENABLE_POLICYD_SPF':'0',
                'ENABLE_QUOTAS':'0','ENABLE_UPDATE_CHECK':'0','PERMIT_DOCKER':'none','SSL_TYPE':'manual',
                'SSL_CERT_PATH':'/certs/cert.pem','SSL_KEY_PATH':'/certs/key.pem','POSTMASTER_ADDRESS':'box-a@ingress.invalid'},
            'volumes':['./data/mail:/var/mail','./data/state:/var/mail-state','./data/logs:/var/log/mail',
                './data/config:/tmp/docker-mailserver','./data/tls:/certs:ro']},
        'postgres':{**common,'image':old['services']['postgres']['image'],'mem_limit':'160m',
            'environment':{'POSTGRES_USER':'ingress','POSTGRES_PASSWORD':pgpass,'POSTGRES_DB':'ingress'},
            'volumes':['./data/postgres:/var/lib/postgresql'],'ports':['15435:5432']},
        },'networks':{'ingress':{'name':PROJECT+'_net'}}}
    write(Path('compose.json'),json.dumps(cfg,indent=2))
    write(Path('private.json'),json.dumps({'mail_password':password,'database_url':f'postgres://ingress:{pgpass}@10.253.24.2:15435/ingress?sslmode=disable'}))
    print(json.dumps({'prepared':True,'images':{k:v['image'] for k,v in cfg['services'].items()}}))
elif mode=='start':
    compose('up','-d','--pull','never')
    for n in range(90):
        try:
            execute('postgres','pg_isready','-U','ingress','-d','ingress')
            execute('mail','doveadm','user','box-a@ingress.invalid')
            execute('mail','doveadm','mailbox','list','-u','box-a@ingress.invalid')
            break
        except subprocess.CalledProcessError:
            if n==89: raise
            time.sleep(2)
    rows=guard();assert len(rows)==2
    print(json.dumps({'ready':True,'containers':len(rows),'dovecot':execute('mail','dovecot','--version').strip()}))
elif mode=='configure':
    rows=guard();assert len(rows)==2 and all(c['State']['Running'] for c in rows)
    cloud=json.loads((ROOT/'cloud-runtime.json').read_text())
    private=json.loads((ROOT/'private.json').read_text())
    mail=next(c for c in rows if c['Config']['Labels']['com.docker.compose.service']=='mail')
    cloud.update(database_url=private['database_url'],spool=str(ROOT/'data/spool'),workspace=str(ROOT),
        docker_root=str(CACHE/'docker'),docker_host=SOCKET,container=mail['Id'],
        targets={'box-a':'box-a@ingress.invalid','box-b':'box-b@ingress.invalid'},ipv4_only=True,http1_only=True)
    write(Path('consumer.json'),json.dumps(cloud))
    print(json.dumps({'configured':True}))
elif mode=='inventory':
    rows=guard()
    print(json.dumps([{'name':c['Name'],'image':c['Image'],'running':c['State']['Running']} for c in rows]))
elif mode=='stop':
    assert len(guard())==2
    compose('stop')
    rows=guard();assert len(rows)==2 and not any(c['State']['Running'] for c in rows)
    print(json.dumps({'stopped':2,'data_preserved':True}))
elif mode=='counts':
    print(json.dumps({x:execute('mail','doveadm','search','-u',f'box-{x}@ingress.invalid','MAILBOX','INBOX','ALL').splitlines() for x in ['a','b']}))
elif mode=='snapshot':
    rows=psql('-At','-c',
        "SELECT COALESCE(json_agg(t),'[]'::json) FROM (SELECT source,id,target,guid,sha256,state,cloud_acked FROM ingress_delivery ORDER BY id) t")
    mail={x:json.loads(execute('mail','doveadm','-f','json','fetch','-u',f'box-{x}@ingress.invalid',
        'guid text','MAILBOX','INBOX','ALL')) for x in ['a','b']}
    result={'ledger':json.loads(rows),'mail':mail}
    write(Path('evidence/latest-snapshot.json'),json.dumps(result,ensure_ascii=False,indent=2))
    print(json.dumps(result,ensure_ascii=False))
elif mode=='forget-ledger':
    # Controlled lost-ledger counterexample; only this dedicated test database.
    assert len(guard())==2
    psql('-c','DELETE FROM ingress_delivery')
    print(json.dumps({'test_ledger_cleared':True}))
elif mode=='reset-synthetic':
    assert len(guard())==2
    for x in ['a','b']:
        execute('mail','doveadm','expunge','-u',f'box-{x}@ingress.invalid','MAILBOX','INBOX','ALL')
    psql('-c','DELETE FROM ingress_delivery')
    print(json.dumps({'test_inboxes_and_ledger_reset':True}))
elif mode=='probe':
    user='box-a@ingress.invalid'
    folder='IngressProbe'
    guid=hashlib.sha256(b'isolated-import-probe-20261025').hexdigest()[:32]
    raw='From: sender@example.net\r\nTo: box-a@ingress.invalid\r\nSubject: probe\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n测试 probe\r\n'
    empty=execute('mail','doveadm','-f','json','fetch','-u',user,'guid text','GUID',guid)
    assert json.loads(empty)==[], 'Probe must start without an existing GUID'
    execute('mail','doveadm','mailbox','create','-u',user,folder)
    execute('mail','doveadm','save','-u',user,'-m',folder,'-g',guid,data=raw.encode())
    data=execute('mail','doveadm','-f','json','fetch','-u',user,'guid text','GUID',guid)
    rows=json.loads(data)
    assert len(rows)==1 and rows[0]['guid']==guid
    assert rows[0]['text'].replace('\r\n','\n')==raw.replace('\r\n','\n')
    result={'guid_query':True,'empty_query_json':True,'canonical_content_equal':True,
            'raw_sha256':hashlib.sha256(raw.encode()).hexdigest(),
            'inbox_count':len(execute('mail','doveadm','search','-u',user,'MAILBOX','INBOX','ALL').splitlines())}
    write(Path('evidence/dovecot-probe.json'),json.dumps(result,indent=2))
    print(json.dumps(result))
elif mode=='cleanup':
    rows=guard()
    for s in ['mail','postgres']: write(Path(f'evidence/{s}.log'),compose('logs','--no-color',s))
    compose('down');assert not guard()
    write(Path('evidence/cleanup.json'),json.dumps({'removed':len(rows),'data_preserved':True}))
    print(json.dumps({'removed':len(rows),'data_preserved':True}))
else: raise SystemExit('Unknown operation')
