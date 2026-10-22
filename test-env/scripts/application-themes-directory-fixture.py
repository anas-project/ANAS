# TEST_CASES: APPTHEME-T-006, APPTHEME-T-008, PORTALUX-T-006
# Finance-only ephemeral identities; run inside the selected Samba container.
import json,sys,os,uuid,re
import ldb
from samba.auth import system_session
from samba.param import LoadParm
from samba.samdb import SamDB
from samba.dcerpc import misc,security
from samba.ndr import ndr_unpack
f=json.load(sys.stdin);assert re.fullmatch(r'pua[0-9a-f]{8}',f['prefix']);marker='ANAS theme acceptance '+f['prefix'];lp=LoadParm();lp.load('/etc/samba/smb.conf');db=SamDB(url='/var/lib/samba/private/sam.ldb',session_info=system_session(),lp=lp);base=str(db.domain_dn());anchor=os.environ['SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE']
def replace(dn,key,value):
 m=ldb.Message();m.dn=ldb.Dn(db,dn);m[key]=ldb.MessageElement(value,ldb.FLAG_MOD_REPLACE,key);db.modify(m)
def identity(dn):
 row=db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['objectGUID','sAMAccountName',anchor])[0];return {'dn':str(row.dn),'guid':str(ndr_unpack(misc.GUID,row['objectGUID'][0])),'username':str(row.get('sAMAccountName',[''])[0]),'anchor':str(row.get(anchor,[''])[0])}
def group(name):
 rows=db.search(base=base,expression='(&(objectClass=group)(cn='+ldb.binary_encode(name)+'))',attrs=['objectSid','memberOf']);assert len(rows)==1;return rows[0]
def ordinary_ancestry(dn):
 queue=[dn];seen=set()
 while queue:
  current=queue.pop();key=current.lower()
  if key in seen:continue
  seen.add(key);row=db.search(base=current,scope=ldb.SCOPE_BASE,attrs=['objectSid','memberOf'])[0];sid=str(ndr_unpack(security.dom_sid,row['objectSid'][0]));rid=int(sid.rsplit('-',1)[1]);assert rid not in [512,518,519,520,544,548,549,550,551,552], 'high privilege fixture forbidden on finance';queue.extend(str(x) for x in row.get('memberOf',[]))
if f['action']=='cleanup':
 removed=[]
 objects=f.get('objects')
 if objects is None:
  owned_ou='OU='+f['prefix']+','+os.environ['SAMBA_DC_BASE_USERS_DN'];rows=db.search(base=base,expression='(&(objectClass=organizationalUnit)(ou='+f['prefix']+'))',attrs=['objectGUID'])
  if rows:
   assert len(rows)==1 and str(rows[0].dn).lower()==owned_ou.lower();rows=db.search(base=owned_ou,scope=ldb.SCOPE_SUBTREE,expression='(description='+ldb.binary_encode(marker)+')',attrs=['objectGUID']);objects=sorted([identity(str(row.dn)) for row in rows],key=lambda x:len(x['dn']))
  else:objects=[]
 for item in reversed(objects):
  row=db.search(base=base,expression='(objectGUID='+ldb.binary_encode(uuid.UUID(item['guid']).bytes_le)+')',attrs=['description'])
  if not row:continue
  assert len(row)==1 and str(row[0].get('description',[''])[0])==marker;db.delete(row[0].dn);removed.append(item['guid'])
 assert not db.search(base=base,expression='(description='+ldb.binary_encode(marker)+')',attrs=['objectGUID']);print(json.dumps({'removed':len(removed),'owned_objects_remaining':0}));raise SystemExit(0)
if f['action']=='require-password-change':
 user=f['users']['normal'];row=db.search(base=base,expression='(objectGUID='+ldb.binary_encode(uuid.UUID(user['guid']).bytes_le)+')',attrs=['description','sAMAccountName','pwdLastSet'])[0]
 assert str(row['description'][0])==marker and str(row['sAMAccountName'][0])==f['prefix']+'normal'
 replace(str(row.dn),'pwdLastSet',['0']);assert str(db.search(base=row.dn,scope=ldb.SCOPE_BASE,attrs=['pwdLastSet'])[0]['pwdLastSet'][0])=='0'
 print(json.dumps({'mandatory_password_change':True,'owned_user':user['guid']}));raise SystemExit(0)
assert f['action']=='prepare';admin=os.environ['SAMBA_DC_ADMIN_GROUP_DN'];ordinary_ancestry(admin);app_all=str(group('APP_all').dn);app_cloud=str(group('APP_nextcloud').dn);ordinary_ancestry(app_all);ordinary_ancestry(app_cloud);ordinary_ancestry(str(group('Domain Users').dn))
parent=os.environ['SAMBA_DC_BASE_USERS_DN'];ou='OU='+f['prefix']+','+parent;assert not db.search(base=base,expression='(ou='+f['prefix']+')',attrs=['objectGUID']);db.create_ou(ou);replace(ou,'description',[marker]);objects=[identity(ou)];users={}
for alias in ['manager','normal']:
 name=f['prefix']+alias;assert not db.search(base=base,expression='(sAMAccountName='+name+')',attrs=['objectGUID']);db.newuser(name,f['passwords'][alias],userou=ou[:-(len(base)+1)],description=marker);dn='CN='+name+','+ou;row=db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['objectGUID'])[0];guid=row['objectGUID'][0];value=str(uuid.UUID(bytes_le=bytes(guid)));replace(dn,'mS-DS-ConsistencyGuid',[guid]);replace(dn,anchor,[value]);replace(dn,'givenName',['Theme']);replace(dn,'sn',['Acceptance']);replace(dn,'displayName',['Theme '+alias]);replace(dn,'mail',[name+'@'+f['mail_domain']]);users[alias]=identity(dn);objects.append(users[alias])
 for group_dn in ([admin,app_all] if alias=='manager' else [app_cloud]):
  m=ldb.Message();m.dn=ldb.Dn(db,group_dn);m['member']=ldb.MessageElement([dn],ldb.FLAG_MOD_ADD,'member');db.modify(m)
print(json.dumps({'prefix':f['prefix'],'users':users,'objects':objects,'domain_privileges':False}))
