#!/usr/bin/env python3
# TEST_CASES: MAILPOC-T-004
"""Run-scoped diagnostics and fault injection, never the default Docker socket."""
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys

root=Path('/home/whl/anas-mail-go-test-20261024/mail-test-go')
os.environ['DOCKER_HOST']='unix:///run/anas-mail-test-1024.sock'
def run(argv, data=None):
    return subprocess.check_output(argv,input=data,stderr=subprocess.PIPE).decode()
assert Path(run(['docker','info','--format','{{.DockerRootDir}}']).strip()).resolve()==root.parent/'docker'
cfg=json.loads((root/'compose.json').read_text())
assert cfg['name']=='anas_mail_test_go_1024'
def compose(*args): return run(['docker','compose','-f',str(root/'compose.json'),*args])
def execute(service,*args): return compose('exec','-T',service,*args)
containers=json.loads(run(['docker','inspect',*compose('ps','-aq').split()]))
assert containers and all(c['Config']['Labels'].get('com.docker.compose.project.working_dir')==str(root) and c['Config']['Labels'].get('anas.mail-poc')=='true' for c in containers)
def save(name,value):
    p=root.parent/'evidence'/name;p.write_text(json.dumps(value,indent=2));p.chmod(0o600)
def sql(query): return execute('postgres','sh','-c','export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -X -U mailgo -d mailgo -At -c "$1"','mail-go-fixture',query)
mode=sys.argv[1]
if mode=='inventory':
    for c in containers:
        for m in c['Mounts']:
            assert m['Type']!='volume'
            if m['Type']=='bind': assert Path(m['Source']).resolve().is_relative_to(root)
    save('inventory.json',[{'name':c['Name'],'image':c['Image'],'mounts':c['Mounts'],'state':c['State']} for c in containers])
    print(json.dumps({'containers':len(containers),'all_scoped':True,'anonymous_volumes':0}))
elif mode=='ad':
    code="""from samba.samdb import SamDB
from samba.auth import system_session
from samba.param import LoadParm
import json
lp=LoadParm();lp.load('/etc/samba/smb.conf')
db=SamDB(url='/var/lib/samba/private/sam.ldb',session_info=system_session(),lp=lp)
row=db.search(base='DC=mailtest,DC=invalid',expression='(sAMAccountName=alice)',attrs=['mail','proxyAddresses'])[0]
print(json.dumps({k:[str(v) for v in row.get(k,[])] for k in ['mail','proxyAddresses']}))
"""
    print(execute('directory','python3','-c',code))
elif mode in ('disable','enable'):
    sql("UPDATE mailboxes SET enabled="+('false' if mode=='disable' else 'true')+" WHERE id='box-01'")
    print(json.dumps({'mailbox_enabled':mode=='enable'}))
elif mode=='rc-state':
    p=list((root/'data/roundcube/db').glob('*.db'));assert len(p)==1
    d=sqlite3.connect('file:'+str(p[0])+'?mode=ro',uri=True);d.row_factory=sqlite3.Row
    result={t:[dict(x) for x in d.execute(q)] for t,q in {'users':'SELECT user_id,username,preferences FROM users ORDER BY user_id','identities':'SELECT user_id,email,signature,standard FROM identities WHERE del=0 ORDER BY user_id,email'}.items()};d.close()
    save('rc-state-'+sys.argv[2]+'.json',result);print(json.dumps({'users':len(result['users']),'saved':True}))
elif mode=='rename-fixture':
    # Explicit pre-provisioned compatibility experiment; not sAMAccountName CRUD.
    code="""from samba.samdb import SamDB
from samba.auth import system_session
from samba.param import LoadParm
import ldb
lp=LoadParm();lp.load('/etc/samba/smb.conf')
db=SamDB(url='/var/lib/samba/private/sam.ldb',session_info=system_session(),lp=lp)
row=db.search(base='DC=mailtest,DC=invalid',expression='(sAMAccountName=alice)',attrs=['anasIdentityAnchor'])[0]
anchor=bytes(row['anasIdentityAnchor'][0]);m=ldb.Message();m.dn=row.dn
m['mail']=ldb.MessageElement('alice.new@mailtest.invalid',ldb.FLAG_MOD_REPLACE,'mail')
m['proxyAddresses']=ldb.MessageElement('smtp:alice@mailtest.invalid',ldb.FLAG_MOD_ADD,'proxyAddresses')
db.modify(m)
assert bytes(db.search(base=row.dn,scope=ldb.SCOPE_BASE,attrs=['anasIdentityAnchor'])[0]['anasIdentityAnchor'][0])==anchor
print('AD anchor preserved')
"""
    execute('directory','python3','-c',code)
    sql("UPDATE mailboxes SET address='alice.new@mailtest.invalid' WHERE id='box-01'; INSERT INTO addresses VALUES('alice.new@mailtest.invalid','box-01',true)")
    compose('restart','manager');print(json.dumps({'synthetic_rename':True,'automatic_samaccountname_workflow':False}))
elif mode in ('stop-go','start-go'):
    compose('stop' if mode=='stop-go' else 'start','manager');print(json.dumps({'manager_running':mode=='start-go'}))
else: raise SystemExit('Unknown mode')
