#!/usr/bin/env python3
"""Formal mail model/service checks on the explicitly approved finance fixture."""
from pathlib import Path
import hashlib
import json
import os
import secrets
import subprocess
import sys
import time
import urllib.request
import urllib.error

ROOT=Path('/home/whl/anas-mail-ingress-test-20261025')
SOCKET='unix:///run/anas-mail-test-1024.sock'
NAME='anas_mail_foundation_1025'
mode=sys.argv[1]
lifecycle=mode.startswith('lifecycle-')
cold=mode.startswith('cold-')
if lifecycle:mode=mode.removeprefix('lifecycle-')
if cold:mode=mode.removeprefix('cold-')
DB='anas_mail_cold_1025' if cold else ('anas_mail_lifecycle_1025' if lifecycle else 'anas_mail_model_1025')
ROLE='anas_mail_cold' if cold else ('anas_mail_lifecycle' if lifecycle else 'anas_mail_model')
os.environ['DOCKER_HOST']=SOCKET
client_config=ROOT/'foundation-docker-client';client_config.mkdir(mode=0o700,exist_ok=True)
os.environ['DOCKER_CONFIG']=str(client_config)
def run(args,data=None):return subprocess.check_output(args,input=data,stderr=subprocess.PIPE).decode()
assert run(['docker','info','--format','{{.DockerRootDir}}']).strip()=='/home/whl/anas-mail-go-test-20261024/docker'
cfg=json.loads((ROOT/'compose.json').read_text());assert cfg['name']=='anas_mail_ingress_1025'
subprocess.run(['python3',str(ROOT/'consumer_server.py'),'inventory'],check=True,stdout=subprocess.DEVNULL)
base=['docker','compose','-f',str(ROOT/'compose.json'),'exec','-T','postgres']
def sql(text):
    return run(base+['sh','-c','export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql "$@"','psql','-h','127.0.0.1','-U','ingress','-d','ingress','-v','ON_ERROR_STOP=1','-At'],text.encode())
def model_sql(text):
    return run(base+['sh','-c','export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql "$@"','psql','-h','127.0.0.1','-U','ingress','-d',DB,'-v','ON_ERROR_STOP=1','-At'],text.encode())
def write(p,value):p.write_text(json.dumps(value,indent=2));p.chmod(0o600)
def connection(password):
    write(ROOT/'mail-model-private.json',{'dsn':f'postgres://{ROLE}:{password}@10.253.24.2:15435/{DB}?sslmode=disable','password':password})
    (ROOT/'mail-model.env').write_text('MAIL_DB_PASSWORD='+password+'\n');(ROOT/'mail-model.env').chmod(0o600)
def container():
    p=subprocess.run(['docker','inspect',NAME],text=True,capture_output=True)
    if p.returncode:return None
    row=json.loads(p.stdout)[0]
    assert row['Config']['Labels']['anas.mail-foundation-workspace']==str(ROOT)
    assert row['Config']['User']=='65532:65532' and row['HostConfig']['ReadonlyRootfs']
    assert row['HostConfig']['CapDrop']==['ALL'] and not row['Mounts']
    return row
def remove():
    if container():run(['docker','rm','-f',NAME])
def start(image):
    assert not container()
    private=json.loads((ROOT/'mail-model-private.json').read_text())
    args=['docker','run','-d','--name',NAME,'--network','anas_mail_ingress_1025_net','--memory','96m','--user','65532:65532','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true',
          '--label','anas.mail-foundation-workspace='+str(ROOT),'-p','28480:8080']
    for key,value in {'HOST':'postgres','PORT':'5432','NAME':DB,'USERNAME':ROLE,'PASSWORD':private['password']}.items():
        # Password uses a private env file, not process argv.
        if key!='PASSWORD':args+=['-e','MAIL_DB_'+key+'='+value]
    args+=['--env-file',str(ROOT/'mail-model.env'),image]
    run(args);assert container()
def http(path):
    try:
        with urllib.request.urlopen('http://10.253.24.2:28480'+path,timeout=3) as r:return r.status,json.load(r)
    except urllib.error.HTTPError as e:return e.code,json.load(e)
if mode=='prepare':
    assert not (ROOT/'mail-model-private.json').exists()
    assert sql("SELECT datname FROM pg_database WHERE datname='"+DB+"';").strip()==''
    assert sql("SELECT rolname FROM pg_roles WHERE rolname='"+ROLE+"';").strip()==''
    password=secrets.token_hex(32)
    sql("CREATE ROLE "+ROLE+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '"+password+"';\nCREATE DATABASE "+DB+" OWNER "+ROLE+";\n")
    connection(password)
    print(json.dumps({'prepared':True,'database':DB,'role_superuser':False,'database_owner':ROLE}))
elif mode=='prepare-upgrade':
    assert not lifecycle and not cold and DB=='anas_mail_model_1025'
    assert not (ROOT/'mail-model-private.json').exists() and not container()
    assert sql("SELECT rolname FROM pg_roles WHERE rolname='anas_mail_model' AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole;").strip()==ROLE
    assert sql("SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='anas_mail_model_1025';").strip()==ROLE
    assert model_sql('SELECT max(version) FROM mail.schema_migration;').strip()=='1'
    assert int(model_sql('SELECT count(*) FROM mail.mailbox;'))>0
    password=secrets.token_hex(32)
    sql("ALTER ROLE anas_mail_model PASSWORD '"+password+"';")
    connection(password)
    print(json.dumps({'upgrade_fixture':True,'database':DB,'starting_schema_version':1,'non_admin_role':True,'rotated_deleted_fixture_credential':True}))
elif mode in ['test-model','test-upgrade']:
    env=dict(os.environ,MAIL_STORE_TEST_CONFIG=str(ROOT/'mail-model-private.json'))
    args=[str(ROOT/'mail-store.test'),'-test.v']
    if cold:args+=['-test.run','^TestPostgreSQLModel$']
    if mode=='test-upgrade':
        assert not lifecycle
        env['MAIL_STORE_TEST_UPGRADE']='1';args+=['-test.run','^TestPostgreSQLUpgrade$']
    p=subprocess.run(args,env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
    evidence=ROOT/'evidence-lifecycle';evidence.mkdir(mode=0o700,exist_ok=True)
    log=evidence/('upgrade-tests.log' if mode=='test-upgrade' else ('cold-model-tests.log' if cold else 'model-tests.log'));log.write_text(p.stdout);log.chmod(0o600)
    print(p.stdout);assert p.returncode==0,'real PostgreSQL model tests failed'
elif mode in ['test-service','test-formal-service']:
    evidence=ROOT/'evidence-lifecycle';evidence.mkdir(mode=0o700,exist_ok=True)
    formal=mode=='test-formal-service'
    image='anas-mail-formal-test:20261025' if formal else 'anas-mail-foundation-poc:20261025'
    if not formal:
        build=ROOT/'foundation-build';build.mkdir(mode=0o700,exist_ok=True)
        binary=ROOT/'anas-mail';assert binary.exists();subprocess.run(['cp',str(binary),str(build/'anas-mail')],check=True)
        (build/'anas-mail').chmod(0o755)
        (build/'Dockerfile').write_text('FROM scratch\nCOPY anas-mail /anas-mail\nUSER 65532:65532\nENTRYPOINT ["/anas-mail"]\n')
        run(['docker','build','-t',image,str(build)])
    image_info=json.loads(run(['docker','image','inspect',image]))[0]
    assert image_info['Config']['User']=='65532:65532' and image_info['Architecture']=='amd64'
    start(image)
    for _ in range(30):
        try:
            if http('/healthz')[0]==200:break
        except OSError:pass
        time.sleep(1)
    else:raise RuntimeError('foundation service did not become healthy')
    if formal:
        binary=ROOT/'formal-image-binary'
        run(['docker','cp',NAME+':/anas-mail',str(binary)])
    report={'image_id':image_info['Id'],'formal_dockerfile':formal,'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'checks':[]}
    run(['docker','exec',NAME,'/anas-mail','healthcheck']);report['binary_healthcheck_passed']=True
    for path,status in [('/healthz',200),('/readyz',503),('/mailboxes',404),('/oidc/callback',404),('/notify',404)]:
        code,body=http(path);assert code==status;report['checks'].append({'path':path,'status':code})
    snapshot=model_sql("SELECT json_build_object('persons',(SELECT count(*) FROM mail.person),'mailboxes',(SELECT count(*) FROM mail.mailbox),'addresses',(SELECT count(*) FROM mail.address),'sessions',(SELECT count(*) FROM mail.web_session),'schema_version',(SELECT max(version) FROM mail.schema_migration),'checksum',(SELECT checksum FROM mail.schema_migration WHERE version=1));")
    report['model']=json.loads(snapshot)
    before=report['model'];remove();start(image)
    for _ in range(30):
        try:
            if http('/healthz')[0]==200:break
        except OSError:pass
        time.sleep(1)
    else:raise RuntimeError('foundation restart failed')
    after=json.loads(model_sql("SELECT json_build_object('persons',(SELECT count(*) FROM mail.person),'mailboxes',(SELECT count(*) FROM mail.mailbox),'addresses',(SELECT count(*) FROM mail.address),'sessions',(SELECT count(*) FROM mail.web_session),'schema_version',(SELECT max(version) FROM mail.schema_migration),'checksum',(SELECT checksum FROM mail.schema_migration WHERE version=1));"))
    assert after==before;report['restart_preserved_model']=True;report['container_guards_verified']=True;report['passed']=True
    write(evidence/('formal-service-results.json' if formal else 'service-results.json'),report);print(json.dumps(report))
elif mode=='cleanup':
    remove()
    for n in ['mail-model-private.json','mail-model.env']:(ROOT/n).unlink(missing_ok=True)
    print(json.dumps({'foundation_container_removed':True,'runtime_credentials_removed':True,'database_preserved':True}))
elif mode=='remove-service':
    remove();print(json.dumps({'foundation_container_removed':True,'database_and_runtime_config_preserved':True}))
else:raise SystemExit('unknown foundation test mode')
