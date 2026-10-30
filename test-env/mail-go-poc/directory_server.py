#!/usr/bin/env python3
# TEST_CASES: MAILPOC-T-005
"""Strictly scoped real AD mutations and faults for the directory experiment."""
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT=Path('/home/whl/anas-mail-go-test-20261024/mail-test-directory')
os.environ['DOCKER_HOST']='unix:///run/anas-mail-test-1024.sock'
def run(argv,data=None):return subprocess.check_output(argv,input=data,stderr=subprocess.PIPE).decode()
assert Path(run(['docker','info','--format','{{.DockerRootDir}}']).strip()).resolve()==ROOT.parent/'docker'
assert json.loads((ROOT/'compose.json').read_text())['name']=='anas_mail_test_go_1024'
def compose(*args):return run(['docker','compose','-f',str(ROOT/'compose.json'),*args])
def execute(service,*args):return compose('exec','-T',service,*args)
containers=json.loads(run(['docker','inspect',*compose('ps','-aq').split()]))
assert containers and all(c['Config']['Labels'].get('com.docker.compose.project.working_dir')==str(ROOT) and c['Config']['Labels'].get('anas.mail-poc')=='true' for c in containers)
def sql(q):return execute('postgres','sh','-c','export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -X -U mailgo -d mailgo -At -c "$1"','mail-directory',q)
mode=sys.argv[1]
if mode in ('disable','enable','remove-direct','restore','nested','remove-nested','profile','delete','add-contact','remove-contact'):
    code='''from samba.samdb import SamDB
from samba.credentials import Credentials
from samba.param import LoadParm
import ldb,json,uuid,sys,time
lp=LoadParm();lp.load('/etc/samba/smb.conf');lp.set('tls verify peer','ca_and_name')
c=Credentials();c.guess(lp);c.set_username('Administrator');c.set_password(json.load(open('/input/secrets.json'))['admin'])
db=SamDB(url='ldaps://dc.mailtest.invalid',credentials=c,lp=lp)
mode=sys.argv[1];marker='Finance Test '+str(time.time_ns())
def modify(uid,attrs):
 row=db.search(base='DC=mailtest,DC=invalid',expression='(&(objectClass=user)(sAMAccountName='+uid+'))',attrs=['userAccountControl'])[0]
 m=ldb.Message();m.dn=row.dn
 for k,v in attrs.items():m[k]=ldb.MessageElement(str(v),ldb.FLAG_MOD_REPLACE,k)
 db.modify(m)
def member(group,user,add):
 row=db.search(base='CN=Users,DC=mailtest,DC=invalid',expression='(&(objectClass=group)(cn='+group+'))',attrs=['member'])[0]
 dn=str(db.search(base='CN=Users,DC=mailtest,DC=invalid',expression='(sAMAccountName='+user+')',attrs=[])[0].dn)
 present=dn.lower() in [str(v).lower() for v in row.get('member',[])]
 if present!=add:db.add_remove_group_members(group,[user],add_members_operation=add)
if mode in ('disable','enable'):
 row=db.search(base='DC=mailtest,DC=invalid',expression='(sAMAccountName=alice)',attrs=['userAccountControl'])[0]
 flags=int(str(row['userAccountControl'][0]));modify('alice',{'userAccountControl':flags|2 if mode=='disable' else flags&~2})
elif mode=='remove-direct':member('MailUsers','alice',False)
elif mode=='restore':member('MailUsers','alice',True);member('Nested','alice',False)
elif mode=='nested':member('Nested','alice',True);member('MailUsers','alice',False)
elif mode=='remove-nested':member('Nested','alice',False)
elif mode=='profile':modify('bob',{'displayName':'Bob Directory Updated','department':marker,'telephoneNumber':'+65 6000 0000'})
elif mode=='delete':db.deleteuser('alice')
elif mode=='add-contact':
 db.newuser('charlie',json.load(open('/input/secrets.json'))['alice'],givenname='Charlie',surname='Directory',mailaddress='charlie@mailtest.invalid')
 row=db.search(base='DC=mailtest,DC=invalid',expression='(sAMAccountName=charlie)',attrs=['objectGUID'])[0]
 modify('charlie',{'anasIdentityAnchor':str(uuid.UUID(bytes_le=bytes(row['objectGUID'][0])))})
 member('MailUsers','charlie',True)
elif mode=='remove-contact':db.deleteuser('charlie')
print(json.dumps({'changed':mode,'marker':marker}))
'''
    print(execute('directory','python3','-c',code,mode))
elif mode=='state':
    print(json.dumps({'directory':sql("SELECT json_build_object('cursor',cursor,'checked',checked,'users',(SELECT count(*) FROM directory_contacts),'groups',(SELECT count(*) FROM directory_groups),'pending',(SELECT count(*) FROM pending_kicks),'sessions',(SELECT count(*) FROM sessions)) FROM directory_state WHERE id=1").strip(),'journal_lines':sum(1 for _ in (ROOT/'data/events/events.jsonl').open())}))
elif mode in ('stop-go','start-go','stop-directory','start-directory'):
    service='manager' if mode.endswith('go') else 'directory'
    compose('stop' if mode.startswith('stop') else 'start',service);print(json.dumps({'mode':mode}))
elif mode in ('pause-go','unpause-go'):
    compose('pause' if mode=='pause-go' else 'unpause','manager');print(json.dumps({'mode':mode}))
elif mode=='reconcile-log':
    lines=compose('logs','--no-log-prefix','manager').splitlines()
    print(json.dumps({'lines':[line for line in lines if 'directory reconciled seq=' in line or 'directory retry pending' in line][-30:]}))
elif mode=='journal-gap':
    path=ROOT/'data/events/events.jsonl';data=path.read_text().splitlines(True)
    assert len(data)>3
    (ROOT/'data/events/events.before-gap').write_text(''.join(data))
    path.write_text(data[-1]);rotated=path.with_name(path.name+'.1')
    if rotated.exists():rotated.rename(rotated.with_name('events.before-gap.1'))
    print(json.dumps({'gap_injected':True,'last_seq':json.loads(data[-1])['seq']}))
elif mode=='inventory':
    for c in containers:
        for m in c['Mounts']:
            assert m['Type']!='volume'
            if m['Type']=='bind':assert Path(m['Source']).resolve().is_relative_to(ROOT)
    out=ROOT.parent/'evidence-directory';out.mkdir(exist_ok=True,mode=0o700)
    (out/'inventory.json').write_text(json.dumps([{'name':c['Name'],'image':c['Image'],'mounts':c['Mounts'],'state':c['State']} for c in containers],indent=2))
    print(json.dumps({'containers':len(containers),'scoped':True}))
else:raise SystemExit('Unknown scoped action')
