#!/usr/bin/env python3
# TEST_CASES: MAILPOC-T-006
"""Offline backup/restore oracle restricted to two named synthetic workspaces."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tarfile
import time

BASE=Path('/home/whl/anas-mail-go-test-20261024')
EVIDENCE=BASE/'evidence-recovery'
SOURCE=BASE/'mail-test-recovery'
TARGET=BASE/'mail-test-recovery-restored'
PROJECT='anas_mail_test_go_recovery_1024'
MID='<mailgo-queued-recovery@mailtest.invalid>'
os.environ['DOCKER_HOST']='unix:///run/anas-mail-test-1024.sock'
parser=argparse.ArgumentParser()
parser.add_argument('mode',choices=['pin','start','inventory','queue-filter','queue-state','snapshot','backup-restore','verify-state','queue-release','cleanup'])
parser.add_argument('--restored',action='store_true')
args=parser.parse_args()
root=TARGET if args.restored else SOURCE
project=PROJECT+'_restored' if args.restored else PROJECT
def run(argv,data=None):
    return subprocess.check_output(argv,input=data,stderr=subprocess.PIPE).decode()
def compose(*argv):return run(['docker','compose','-f',str(root/'compose.json'),*argv])
def execute(service,*argv):return compose('exec','-T',service,*argv)
def save(name,value):
    path=EVIDENCE/name;path.write_text(json.dumps(value,indent=2));path.chmod(0o600)
def sql(q):return execute('postgres','sh','-c','export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -X -U mailgo -d mailgo -At -c "$1"','mail-recovery',q).strip()
def containers():
    ids=compose('ps','-aq').split()
    rows=json.loads(run(['docker','inspect',*ids])) if ids else []
    for c in rows:
        labels=c['Config']['Labels']
        assert labels['com.docker.compose.project']==project and labels['com.docker.compose.project.working_dir']==str(root)
        assert labels.get('anas.mail-poc')=='true'
        for m in c['Mounts']:
            assert m['Type']!='volume'
            if m['Type']=='bind':assert Path(m['Source']).resolve().is_relative_to(root)
    return rows
def rc_state():
    files=list((root/'data/roundcube/db').glob('*.db'));assert len(files)==1
    db=sqlite3.connect('file:'+str(files[0])+'?mode=ro',uri=True);db.row_factory=sqlite3.Row
    result={table:[dict(row) for row in db.execute(q)] for table,q in {
        'users':'SELECT user_id,username,mail_host,preferences FROM users ORDER BY user_id',
        'identities':'SELECT identity_id,user_id,standard,name,email,organization,signature FROM identities WHERE del=0 ORDER BY user_id,email',
    }.items()};db.close();return result
def pg_state():
    result={}
    for table,key in [('people','anchor'),('mailboxes','id'),('addresses','address'),('directory_contacts','id'),('directory_groups','id')]:
        result[table]=json.loads(sql(f"SELECT coalesce(json_agg(t ORDER BY {key}),'[]') FROM {table} t"))
    result['cursor']=int(sql('SELECT cursor FROM directory_state WHERE id=1'))
    return result
def queue():return [json.loads(line) for line in execute('mail','postqueue','-j').splitlines()]
def held():
    rows=queue();assert len(rows)==1 and rows[0]['queue_name']=='hold'
    q=rows[0];body=execute('mail','postcat','-q',q['queue_id'])
    assert 'Message-ID: '+MID in body
    assert q['sender']=='shopping@mailtest.invalid'
    assert [r['address'] for r in q['recipients']]==['box-01@boxes.mailtest.invalid']
    assert 'original_recipient: work@mailtest.invalid' in body.splitlines()
    return {'queue_id':q['queue_id'],'message_id':MID,'sender':q['sender'],'original_recipient':'work@mailtest.invalid','recipients':q['recipients']}
def file_manifest(directory):
    result={}
    for item in sorted(directory.rglob('*')):
        stat=item.lstat();name=str(item.relative_to(directory))
        if item.is_symlink():result[name]={'link':os.readlink(item)}
        elif item.is_file():result[name]={'sha256':hashlib.file_digest(item.open('rb'),'sha256').hexdigest(),'mode':stat.st_mode&0o7777,'uid':stat.st_uid,'gid':stat.st_gid}
    return result
def start():
    compose('create');containers()
    compose('start','directory','postgres')
    for _ in range(60):
        try:
            execute('directory','python3','-c','import socket; socket.create_connection(("127.0.0.1",636),2).close()')
            if (root/'data/samba/.mail-poc-ready').exists():break
        except subprocess.CalledProcessError:pass
        time.sleep(1)
    else:raise RuntimeError('AD not ready')
    compose('start','iam','webmail','mail','manager')
    compose('start','proxy')
    for _ in range(90):
        try:
            run(['curl','--noproxy','*','--silent','--fail','--max-time','3','--cacert',str(root/'data/tls/cert.pem'),'--resolve','manage.mailtest.invalid:18884:10.253.24.2','https://manage.mailtest.invalid:18884/health'])
            execute('mail','python3','-c','import socket; [socket.create_connection(("127.0.0.1",p),2).close() for p in (993,587)]')
            print(json.dumps({'ready':True,'workspace':str(root)}));return
        except subprocess.CalledProcessError:time.sleep(1)
    raise RuntimeError('Mail/Go readiness deadline exceeded')

assert Path(run(['docker','info','--format','{{.DockerRootDir}}']).strip()).resolve()==BASE/'docker'
assert root.resolve() in (SOURCE,TARGET) and json.loads((root/'compose.json').read_text())['name']==project
EVIDENCE.mkdir(exist_ok=True,mode=0o700)
containers()
if args.mode=='pin':
    assert not containers()
    old=json.loads((BASE/'evidence-directory/inventory.json').read_text())
    cfg=json.loads((root/'compose.json').read_text())
    for service in cfg['services']:
        image=next(c['image'] for c in old if c['name'].endswith('-'+service+'-1'))
        cfg['services'][service]['image']=image
    (root/'compose.json').write_text(json.dumps(cfg,indent=2))
    assert hashlib.file_digest((root/'fixture/mail-go').open('rb'),'sha256').hexdigest()=='7287d2f4194c08b80fb73c07107d1df353c8907a4663084a64bd1dfdacd99e0b'
    print(json.dumps({'fixed_images':len(cfg['services']),'binary_verified':True}))
elif args.mode=='start':start()
elif args.mode=='inventory':
    rows=containers();assert len(rows)==7
    save(('restored-' if args.restored else '')+'inventory.json',[{'name':c['Name'],'image':c['Image'],'mounts':c['Mounts'],'state':c['State']['Status']} for c in rows])
    print(json.dumps({'containers':len(rows),'scoped':True}))
elif args.mode=='queue-filter':
    assert not queue()
    p=root/'data/dms/config/recovery-hold.regexp';p.write_text('/^Message-ID: '+MID+'$/ HOLD mail Go recovery\n');p.chmod(0o644)
    execute('mail','postconf','-P','sender-cleanup/unix/header_checks=regexp:/tmp/docker-mailserver/recovery-hold.regexp')
    execute('mail','postfix','reload');print(json.dumps({'hold_filter':True}))
elif args.mode=='queue-state':
    state=held();save(('restored-' if args.restored else '')+'queue.json',state);print(json.dumps(state))
elif args.mode=='snapshot':
    state={'roundcube':rc_state(),'postgres':pg_state()}
    assert len(state['roundcube']['users'])==3
    assert state['postgres']['cursor']>0 and len(state['postgres']['mailboxes'])==3
    assert any('subject' in u['preferences'] for u in state['roundcube']['users'])
    save('application-before.json',state);print(json.dumps({'baseline_saved':True,'cursor':state['postgres']['cursor']}))
elif args.mode=='backup-restore':
    assert not args.restored and not TARGET.exists()
    q=held()
    # Quiesce users before writers and persistence dependencies. The candidate
    # IAM's PID 1 is a shell without TERM forwarding; nginx owns its foreground
    # lifetime, so use nginx's native graceful shutdown before Docker stop.
    for service in ('proxy','webmail','mail','manager'):
        compose('stop','--timeout','45',service)
    execute('iam','nginx','-s','quit')
    for _ in range(20):
        if not any(c['Config']['Labels']['com.docker.compose.service']=='iam' and c['State']['Running'] for c in containers()):break
        time.sleep(1)
    compose('stop','--timeout','45','iam')
    execute('directory','smbcontrol','all','shutdown')
    compose('stop','--timeout','45','directory')
    compose('stop','--timeout','45','postgres')
    stopped=containers()
    save('stopped-services.json',[{'name':c['Name'],'state':c['State']} for c in stopped])
    assert all(c['State']['Status']=='exited' and c['State']['ExitCode']!=137 and not c['State']['OOMKilled'] for c in stopped)
    assert all(c['State']['ExitCode']==0 for c in stopped if c['Config']['Labels']['com.docker.compose.service'] in ('mail','postgres','iam'))
    before=file_manifest(root/'data');save('stopped-data-manifest.json',before)
    archive=EVIDENCE/'stopped-workspace.tar';assert not archive.exists()
    with tarfile.open(archive,'w') as tar:
        for name in ('data','fixture','compose.json'):tar.add(root/name,arcname=name)
    archive.chmod(0o600);digest=hashlib.file_digest(archive.open('rb'),'sha256').hexdigest()
    TARGET.mkdir(mode=0o700)
    with tarfile.open(archive) as tar:tar.extractall(TARGET,filter='fully_trusted')
    after=file_manifest(TARGET/'data');save('extracted-data-manifest.json',after)
    assert before==after,'Restored data bytes/ownership/mode mismatch'
    # This archive is locally generated from the closed synthetic tree only.
    cfg=json.loads((TARGET/'compose.json').read_text());cfg['name']=PROJECT+'_restored';cfg['networks']['mailpoc']['name']=PROJECT+'_restored_mailpoc'
    (TARGET/'compose.json').write_text(json.dumps(cfg,indent=2))
    compose('down')
    save('restore.json',{'sha256':digest,'bytes':archive.stat().st_size,'target_previously_absent':True,'source_preserved':root.exists(),'data_files_exact':len(before),'queue':q})
    print(json.dumps({'restored_to_empty_directory':True,'sha256':digest,'bytes':archive.stat().st_size}))
elif args.mode=='verify-state':
    assert args.restored
    before=json.loads((EVIDENCE/'application-before.json').read_text())
    after={'roundcube':rc_state(),'postgres':pg_state()};save('application-after.json',after)
    checks={
        'roundcube_users_identities_preferences_equal':before['roundcube']==after['roundcube'],
        'postgres_owners_mailboxes_addresses_contacts_equal':all(before['postgres'][t]==after['postgres'][t] for t in ('people','mailboxes','addresses','directory_contacts','directory_groups')),
        'directory_cursor_preserved':after['postgres']['cursor']>=before['postgres']['cursor']>0,
        'event_journal_preserved':(root/'data/events/events.jsonl').read_bytes().startswith((SOURCE/'data/events/events.jsonl').read_bytes()),
    };save('application-restore-result.json',checks);print(json.dumps(checks));assert all(checks.values())
elif args.mode=='queue-release':
    assert args.restored
    q=held();before=json.loads((EVIDENCE/'queue.json').read_text())
    assert all(q[k]==before[k] for k in ('message_id','sender','original_recipient','recipients'))
    # Postfix short queue IDs depend on inode numbers; relocation can rename
    # queue files. Compare the original queue's actual RFC message, not its ID.
    original=SOURCE/'data/dms/state/spool-postfix/hold'/before['queue_id']
    code='''import sys,tempfile,subprocess,hashlib
with tempfile.NamedTemporaryFile() as f:
 f.write(sys.stdin.buffer.read());f.flush()
 raw=subprocess.check_output(['postcat','-bh',f.name])
 print(hashlib.sha256(raw).hexdigest())
'''
    original_hash=run(['docker','compose','-f',str(root/'compose.json'),'exec','-T','mail','python3','-c',code],original.read_bytes()).strip()
    restored_hash=hashlib.sha256(execute('mail','postcat','-bhq',q['queue_id']).encode()).hexdigest()
    assert original_hash==restored_hash
    execute('mail','postsuper','-H',q['queue_id']);execute('mail','postqueue','-i',q['queue_id'])
    for _ in range(30):
        if not queue():break
        time.sleep(1)
    assert not queue();result={'same_original_message':True,'empty_after_delivery':True,'original_queue_id':before['queue_id'],'restored_queue_id':q['queue_id'],'message_sha256':restored_hash}
    save('queue-release.json',result);print(json.dumps(result))
elif args.mode=='cleanup':
    rows=containers();assert rows
    for service in ('manager','mail','webmail','directory','postgres'):
        p=EVIDENCE/(('restored-' if args.restored else '')+service+'.log');p.write_text(compose('logs','--no-color',service));p.chmod(0o600)
    compose('down');assert not containers()
    save('compose-cleanup.json',{'containers_removed':len(rows),'source_preserved':SOURCE.exists(),'restored_preserved':TARGET.exists()})
    print(json.dumps({'containers_removed':len(rows),'data_preserved':True}))
