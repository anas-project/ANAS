#!/usr/bin/env python3
"""Maintenance operations restricted to a generated mail PoC workspace/daemon."""
# TEST_CASES: MAILPOC-T-001, MAILPOC-T-002, MAILPOC-T-003
import argparse
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tarfile
import time

parser=argparse.ArgumentParser()
parser.add_argument('--workspace',type=Path,required=True)
parser.add_argument('mode',choices=['inventory','queue-filter','queue-release','backup-restore','webmail-state','verify-webmail','verify-rename','remove-alias','rename'])
args=parser.parse_args()
root=args.workspace.resolve()
if not root.is_absolute() or not root.name.startswith('mail-test-') or 'anas-mail-webmail-test-' not in str(root.parent):
    raise SystemExit('Only the explicit mail test workspace is allowed')
socket=os.environ.get('DOCKER_HOST','')
if not socket.startswith('unix:///run/anas-mail-test-') or not socket.endswith('.sock'):
    raise SystemExit('A dedicated mail test Docker socket is required')
cfg=json.loads((root/'compose.json').read_text())
project=cfg['name']
if not project.startswith('anas_mail_test_'):
    raise SystemExit('Unexpected compose project')
evidence=root.parent/'evidence'; evidence.mkdir(exist_ok=True,mode=0o700)


def command(argv):
    return subprocess.check_output(argv,stderr=subprocess.PIPE).decode()


def compose(*argv):
    return command(['docker','compose','-f',str(root/'compose.json'),*argv])


def execute(service,*argv):
    return compose('exec','-T',service,*argv)


def save(name,data):
    path=evidence/name
    path.write_text(json.dumps(data,indent=2,ensure_ascii=False)); path.chmod(0o600)


def wait_mail():
    for _ in range(45):
        try:
            execute('mail','python3','-c','import socket; [socket.create_connection(("127.0.0.1",p),2).close() for p in (993,587)]')
            return
        except subprocess.CalledProcessError:
            time.sleep(2)
    raise RuntimeError('Mail protocols did not become ready')


def webmail_state():
    candidates=list((root/'data/roundcube/db').glob('*.db'))
    assert len(candidates)==1, 'Expected one SQLite database'
    db=sqlite3.connect('file:'+str(candidates[0])+'?mode=ro',uri=True)
    db.row_factory=sqlite3.Row
    state={table:[dict(row) for row in db.execute(query)] for table,query in {
        'users':'SELECT user_id,username,mail_host,preferences FROM users ORDER BY user_id',
        'identities':'SELECT identity_id,user_id,standard,name,email,organization,signature FROM identities WHERE del=0 ORDER BY user_id,email',
    }.items()}
    db.close()
    return state


actual_root=command(['docker','info','--format','{{.DockerRootDir}}']).strip()
assert Path(actual_root).resolve()==root.parent/'docker', 'Wrong Docker data root'
ids=compose('ps','-aq').split()
assert ids, 'Expected existing test containers'
containers=json.loads(command(['docker','inspect',*ids]))
assert all(c['Config']['Labels'].get('anas.mail-poc')=='true' and c['Config']['Labels'].get('com.docker.compose.project')==project for c in containers)

if args.mode=='inventory':
    rows=[]
    for container in containers:
        mounts=container['Mounts']
        assert all(m['Type']!='volume' for m in mounts), 'Anonymous persistent volume found'
        for m in mounts:
            if m['Type']=='bind':
                assert Path(m['Source']).resolve().is_relative_to(root), 'Mount outside test workspace'
        rows.append({'name':container['Name'],'image':container['Image'],'mounts':mounts,'state':container['State']['Status']})
    save(project+'-inventory.json',rows)
    print(json.dumps({'success':True,'containers':len(rows),'anonymous_volumes':0,'docker_root':actual_root}))
elif args.mode=='queue-filter':
    assert not execute('mail','postqueue','-j').strip(), 'Queue must be empty before fixture'
    p=root/'data/dms/config/poc-hold.regexp'
    p.write_text('/^Message-ID: <mail-poc-queued-restore@mailtest.invalid>$/ HOLD mail PoC recovery\n'); p.chmod(0o644)
    execute('mail','postconf','-P','sender-cleanup/unix/header_checks=regexp:/tmp/docker-mailserver/poc-hold.regexp')
    execute('mail','postfix','reload')
    print(json.dumps({'success':True,'queue_fixture_filter':True}))
elif args.mode=='queue-release':
    queue=[json.loads(line) for line in execute('mail','postqueue','-j').splitlines()]
    assert len(queue)==1 and queue[0]['queue_name']=='hold', 'Expected exactly one restored held message'
    entry=queue[0]
    assert 'Message-ID: <mail-poc-queued-restore@mailtest.invalid>' in execute('mail','postcat','-q',entry['queue_id'])
    assert entry['sender']=='alice@mailtest.invalid'
    assert all(r['address']=='alice@mailtest.invalid' for r in entry['recipients'])
    execute('mail','postsuper','-H',entry['queue_id'])
    execute('mail','postqueue','-i',entry['queue_id'])
    save('queue-release.json',{'queue_id':entry['queue_id'],'success':True})
    print(json.dumps({'success':True,'released_messages':1}))
elif args.mode=='webmail-state':
    state=webmail_state()
    save(project+'-webmail-state.json',state)
    print(json.dumps({'success':True,'users':len(state['users']),'identities':len(state['identities'])}))
elif args.mode=='verify-webmail':
    expected=json.loads((evidence/'webmail-before-restore.json').read_text())
    actual=webmail_state()
    result={'success':actual==expected,'users_identities_preferences_match':actual==expected}
    save('webmail-restored.json',actual)
    save('webmail-restore-result.json',result)
    print(json.dumps(result))
    raise SystemExit(0 if result['success'] else 1)
elif args.mode=='verify-rename':
    expected=json.loads((evidence/'webmail-before-restore.json').read_text())
    actual=webmail_state()
    before=next(u for u in expected['users'] if u['username']=='alice@mailtest.invalid')
    after=next(u for u in actual['users'] if u['username']=='alice.new@mailtest.invalid')
    old_aliases=[i for i in expected['identities'] if i['user_id']==before['user_id'] and i['email'] in ('shopping@mailtest.invalid','work@mailtest.invalid')]
    new_aliases=[i for i in actual['identities'] if i['user_id']==after['user_id'] and i['email'] in ('shopping@mailtest.invalid','work@mailtest.invalid')]
    checks={'same_user_id':before['user_id']==after['user_id'],'preferences_preserved':before['preferences']==after['preferences'],'alias_identities_preserved':old_aliases==new_aliases}
    result={'success':all(checks.values()),'checks':checks,'before_user_id':before['user_id'],'after_user_id':after['user_id']}
    save('webmail-after-rename.json',actual); save('identity-continuity-result.json',result)
    print(json.dumps(result))
    raise SystemExit(0 if result['success'] else 1)
elif args.mode=='backup-restore':
    queue=[json.loads(line) for line in execute('mail','postqueue','-j').splitlines()]
    assert len(queue)==1 and queue[0]['queue_name']=='hold', 'Held recovery message required'
    assert 'Message-ID: <mail-poc-queued-restore@mailtest.invalid>' in execute('mail','postcat','-q',queue[0]['queue_id'])
    target=root.parent/'mail-test-restored'
    assert not target.exists(), 'Restore target must be absent'
    compose('stop')
    state=webmail_state()
    save('webmail-before-restore.json',state)
    archive=evidence/'stopped-workspace.tar'
    assert not archive.exists(), 'Refusing to overwrite previous backup'
    with tarfile.open(archive,'w') as tar:
        for name in ('data','fixture','compose.json'):
            tar.add(root/name,arcname=name)
    archive.chmod(0o600)
    digest=hashlib.file_digest(archive.open('rb'),'sha256').hexdigest()
    target.mkdir(mode=0o700)
    with tarfile.open(archive) as tar:
        tar.extractall(target,filter='fully_trusted')  # Locally generated, closed test tree only.
    restored=json.loads((target/'compose.json').read_text())
    restored['name']=project+'_restored'
    restored['networks']['mailpoc']['name']=project+'_restored_mailpoc'
    (target/'compose.json').write_text(json.dumps(restored,indent=2))
    # Old test containers keep their original data, but must release test ports.
    compose('down')
    output=command(['docker','compose','-f',str(target/'compose.json'),'up','-d'])
    save('restore.json',{'archive':str(archive),'sha256':digest,'target':str(target),'held_queue_id':queue[0]['queue_id']})
    print(json.dumps({'success':True,'restore_started':True,'archive_sha256':digest,'target':str(target)}))
elif args.mode=='remove-alias':
    p=root/'data/dms/config/postfix-virtual.cf'
    p.write_text(''.join(line for line in p.read_text().splitlines(True) if not line.startswith('shopping@mailtest.invalid ')))
    compose('restart','mail')
    wait_mail()
    print(json.dumps({'success':True,'removed_alias':'shopping@mailtest.invalid','automatic_ad_sync':False}))
elif args.mode=='rename':
    # Explicit test provisioning, NOT production directory synchronization.
    p=root/'data/dms/config/postfix-accounts.cf'
    p.write_text(p.read_text().replace('alice@mailtest.invalid|','alice.new@mailtest.invalid|'))
    p=root/'data/dms/config/postfix-virtual.cf'
    p.write_text(p.read_text().replace(' alice@mailtest.invalid',' alice.new@mailtest.invalid'))
    p=root/'fixture/rename.py'
    p.write_text('''from samba.samdb import SamDB
from samba.auth import system_session
from samba.param import LoadParm
import ldb
lp=LoadParm(); lp.load('/etc/samba/smb.conf')
db=SamDB(url='/var/lib/samba/private/sam.ldb',session_info=system_session(),lp=lp)
row=db.search(base='DC=mailtest,DC=invalid',expression='(sAMAccountName=alice)',attrs=['anasIdentityAnchor'])[0]
msg=ldb.Message(); msg.dn=row.dn
msg['mail']=ldb.MessageElement('alice.new@mailtest.invalid',ldb.FLAG_MOD_REPLACE,'mail')
db.modify(msg)
assert bytes(db.search(base=row.dn,scope=ldb.SCOPE_BASE,attrs=['anasIdentityAnchor'])[0]['anasIdentityAnchor'][0])==bytes(row['anasIdentityAnchor'][0])
print('stable directory anchor confirmed')
''')
    execute('directory','python3','/fixture/rename.py')
    compose('restart','mail')
    wait_mail()
    print(json.dumps({'success':True,'new_primary':'alice.new@mailtest.invalid','stable_directory_anchor':True,'automatic_ad_sync':False}))
