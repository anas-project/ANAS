#!/usr/bin/env python3
# TEST_CASES: PORTAL-T-002, PORTAL-T-003, PORTAL-T-004
"""Mutate only the private portal fixture; never print passwords or bind data."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys

root = Path(sys.argv[1])
action = sys.argv[2]
if not re.fullmatch(r'/home/whl/anas-user-portal-e2e-test-[A-Za-z0-9-]+', str(root)):
    raise SystemExit('isolated fixture root required')
run = json.loads((root / 'run.json').read_text())
prefix = run['prefix']
socket = run['socket']
assert re.fullmatch(r'anas_pu[0-9a-f]{8}_', prefix)
assert socket == '/run/' + root.name + '.sock'
assert os.environ.get('DOCKER_HOST', 'unix://' + socket) == 'unix://' + socket
container = prefix + 'samba_dc'
info = json.loads(subprocess.check_output(['docker', '-H', 'unix://' + socket, 'inspect', container]))[0]
assert info['Name'] == '/' + container
fixture_path = root / 'private/ldap.json'
assert fixture_path.resolve() == fixture_path and not fixture_path.is_symlink()
fixture = json.loads(fixture_path.read_text())
# Code sent through stdin executes inside the selected fixture container only.
code = r'''
import json,sys,time
import ldb
from samba.samdb import SamDB
from samba.auth import system_session
from samba.param import LoadParm
lp=LoadParm();lp.load('/etc/samba/smb.conf')
db=SamDB(url='/var/lib/samba/private/sam.ldb',session_info=system_session(),lp=lp)
f=json.loads(sys.argv[1]);action=sys.argv[2];base=f['base'];u=f['users'];g=f['groups']
assert base.lower()=='dc=portal,dc=test'
def replace(dn,attr,values):
 m=ldb.Message();m.dn=ldb.Dn(db,dn);m[attr]=ldb.MessageElement(values,ldb.FLAG_MOD_REPLACE,attr);db.modify(m)
def members(dn):return [str(x) for x in db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['member'])[0].get('member',[])]
def member(dn,user,add):
 v=members(dn);v=[x for x in v if x.lower()!=user.lower()]
 if add:v.append(user)
 replace(dn,'member',v)
def named_group(name):return str(db.search(base=base,expression='(&(objectClass=group)(cn='+name+'))',attrs=['cn'])[0].dn)
if action in ['lock-target','lock-operator']:
 replace(u['poperator' if action=='lock-operator' else 'pscopea'],'lockoutTime',[str(int((time.time()+11644473600)*10000000))])
elif action=='restore-operator-lock':replace(u['poperator'],'lockoutTime',['0'])
elif action=='expire-target':
 dn='CN=portal_fixture_expiry,CN=Password Settings Container,CN=System,'+base
 db.add({'dn':dn,'objectClass':['top','msDS-PasswordSettings'],'msDS-PasswordSettingsPrecedence':'1','msDS-PasswordReversibleEncryptionEnabled':'FALSE','msDS-PasswordHistoryLength':'5','msDS-PasswordComplexityEnabled':'TRUE','msDS-MinimumPasswordLength':'8','msDS-MinimumPasswordAge':'0','msDS-MaximumPasswordAge':'-10000000','msDS-LockoutThreshold':'10','msDS-LockoutObservationWindow':'-18000000000','msDS-LockoutDuration':'-18000000000','msDS-PSOAppliesTo':u['pexpired']})
elif action=='restore-expiry':
 dn='CN=portal_fixture_expiry,CN=Password Settings Container,CN=System,'+base
 if db.search(base=base,expression='(distinguishedName='+dn+')',attrs=['cn']):db.delete(dn)

elif action=='restore-initial-memberships':replace(g['PortalAllowed'],'member',[g['PortalNested']])
elif action in ['remove-app','restore-app']:member(named_group('FS Share RW'),u['pcasuser'],action=='restore-app')
elif action=='recreate-observer':
 import uuid
 dn=u['pcasuser'];db.delete(dn);db.newuser('pcasuser','Aa1!'+uuid.uuid4().hex,userou='OU=PortalA,OU=People')
 raw=db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['objectGUID'])[0]['objectGUID'][0]
 replace(dn,'mS-DS-ConsistencyGuid',[raw]);replace(dn,'anasIdentityAnchor',[str(uuid.UUID(bytes_le=raw))])
elif action=='recreate-editable-group':
 import uuid
 dn=g['PortalAllowed'];db.delete(dn);db.newgroup('PortalAllowed',groupou='OU=Access,OU=Groups')
 raw=db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['objectGUID'])[0]['objectGUID'][0]
 replace(dn,'mS-DS-ConsistencyGuid',[raw]);replace(dn,'anasIdentityAnchor',[str(uuid.UUID(bytes_le=raw))])
elif action=='editable-group-empty':
 print(json.dumps({'empty':not members(g['PortalAllowed'])}));sys.exit(0)
elif action=='rename-observer':
 replace(u['pcasuser'],'sAMAccountName',['pcas-renamed']);replace(u['pcasuser'],'displayName',['Renamed fixture'])
elif action=='disable-observer':
 flags=int(db.search(base=u['pcasuser'],scope=ldb.SCOPE_BASE,attrs=['userAccountControl'])[0]['userAccountControl'][0]);replace(u['pcasuser'],'userAccountControl',[str(flags|2)])
elif action=='restore-observer':
 flags=int(db.search(base=u['pcasuser'],scope=ldb.SCOPE_BASE,attrs=['userAccountControl'])[0]['userAccountControl'][0]);replace(u['pcasuser'],'userAccountControl',[str(flags&~2)]);replace(u['pcasuser'],'sAMAccountName',['pcasuser']);replace(u['pcasuser'],'displayName',[])
elif action in ['remove-operator','restore-operator']:member(g['PortalOperators'],u['poperator'],action=='restore-operator')
elif action=='snapshot':
 out={}
 for name,dn in u.items():
  row=db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['lockoutTime','pwdLastSet','userAccountControl','accountExpires'])[0]
  out[name]={a:str(row[a][0]) for a in ['lockoutTime','pwdLastSet','userAccountControl','accountExpires'] if a in row}
 print(json.dumps(out));sys.exit(0)
elif action=='recovery-alias':
 row=db.search(base=u['pscopeb'],scope=ldb.SCOPE_BASE,attrs=['sAMAccountName','userPrincipalName','anasIdentityAnchor'])[0]
 print(json.dumps({'username':str(row['sAMAccountName'][0]),'upn':str(row['userPrincipalName'][0]),'anchor':str(row['anasIdentityAnchor'][0])}));sys.exit(0)
else:raise SystemExit('unknown fixture action')
print(json.dumps({'action':action,'applied':True}))
'''
# Only the needed references are transmitted as arguments; no credentials.
references = {k: fixture[k] for k in ['base', 'users', 'groups']}
result = subprocess.run(['docker', '-H', 'unix://' + socket, 'exec', '-i', container, 'python3', '-', json.dumps(references), action], input=code.encode(), capture_output=True, check=True)
sys.stdout.buffer.write(result.stdout)
