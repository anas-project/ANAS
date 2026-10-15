#!/usr/bin/env python3
# TEST_CASES: PORTAL-T-002, PORTAL-T-003, PORTAL-T-004, PORTAL-T-005
"""Initialize only an isolated portal.test directory; private output holds credentials."""
import os,json,time,uuid,base64,re
import ldb
from samba.samdb import SamDB
from samba.auth import system_session
from samba.param import LoadParm
from samba.ndr import ndr_unpack
from samba.dcerpc import security,misc
assert re.fullmatch(r'anas-user-portal-e2e-test-[A-Za-z0-9-]+',os.environ.get('PORTAL_TEST_FIXTURE','')), 'isolated fixture required'
lp=LoadParm();lp.load('/etc/samba/smb.conf')
db=SamDB(url='/var/lib/samba/private/sam.ldb',session_info=system_session(),lp=lp)
base=str(db.domain_dn());assert base.lower()=='dc=portal,dc=test', 'test directory required'
schema=str(db.get_schema_basedn())
def replace(dn,attr,values):
 m=ldb.Message();m.dn=ldb.Dn(db,dn);m[attr]=ldb.MessageElement(values,ldb.FLAG_MOD_REPLACE,attr);db.modify(m)
def sid(name):
 v=db.search(base=base,scope=ldb.SCOPE_SUBTREE,expression='(sAMAccountName='+name+')',attrs=['objectSid'])[0]
 return str(ndr_unpack(security.dom_sid,v['objectSid'][0]))
def ace(dn,text):
 sd=db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['nTSecurityDescriptor'],controls=['sd_flags:1:4'])[0]['nTSecurityDescriptor'][0]
 descriptor=ndr_unpack(security.descriptor,sd)
 new=security.descriptor.from_sddl('D:'+text,security.dom_sid(db.get_domain_sid()))
 descriptor.dacl_add(new.dacl.aces[0])
 from samba.ndr import ndr_pack
 m=ldb.Message();m.dn=ldb.Dn(db,dn);m['nTSecurityDescriptor']=ldb.MessageElement([ndr_pack(descriptor)],ldb.FLAG_MOD_REPLACE,'nTSecurityDescriptor');db.modify(m,controls=['sd_flags:1:4'])
def guid(attr):
 v=db.search(base=schema,scope=ldb.SCOPE_SUBTREE,expression='(lDAPDisplayName='+attr+')',attrs=['schemaIDGUID'])[0]
 return str(ndr_unpack(misc.GUID,v['schemaIDGUID'][0]))
people='OU=People,'+base;ga='OU=Access,OU=Groups,'+base
for ou in ['PortalA','PortalB']:
 if not db.search(base=base,expression='(distinguishedName=OU='+ou+','+people+')'): db.create_ou('OU='+ou+','+people)
pw='Up-'+uuid.uuid4().hex+'!aA9'
newpw='New-'+uuid.uuid4().hex+'!aA9'
users={}
for name,ou in [('pnormal','PortalA'),('pfirst','PortalA'),('pexpired','PortalA'),('pscopea','PortalA'),('pscopeb','PortalB'),('pprotected','PortalA'),('poperator','PortalA'),('pcasuser','PortalA')]:
 dn='CN='+name+',OU='+ou+','+people
 if not db.search(base=base,expression='(sAMAccountName='+name+')'): db.newuser(name,pw,userou='OU='+ou+',OU=People')
 else: db.setpassword('(sAMAccountName='+name+')',pw,force_change_at_next_login=False)
 raw=db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['objectGUID'])[0]['objectGUID'][0]
 replace(dn,'mS-DS-ConsistencyGuid',[raw]);replace(dn,'anasIdentityAnchor',[str(uuid.UUID(bytes_le=raw))])
 replace(dn,'userPrincipalName',[name+'@portal.test'])
 users[name]=dn
for name in ['svc_portal_reset','svc_portal_group']:
 if not db.search(base=base,expression='(sAMAccountName='+name+')'): db.newuser(name,pw,userou='OU=Service Accounts')
 else: db.setpassword('(sAMAccountName='+name+')',pw,force_change_at_next_login=False)
 dn='CN='+name+',OU=Service Accounts,'+base
 replace(dn,'userAccountControl',['66048'])
services={'reset':{'dn':'CN=svc_portal_reset,OU=Service Accounts,'+base,'password':pw},'group':{'dn':'CN=svc_portal_group,OU=Service Accounts,'+base,'password':pw},
'readonly':{'dn':os.environ['SAMBA_DC_LDAP_BIND_DN'],'password':os.environ['SAMBA_DC_LDAP_BIND_PASSWORD']},
'shared_reset':{'dn':os.environ['SAMBA_DC_PASSWORD_BIND_DN'],'password':os.environ['SAMBA_DC_PASSWORD_BIND_PASSWORD']}}
for v in services.values():
 # Keep service binds available during the one-second expiry experiment.
 flags=int(db.search(base=v['dn'],scope=ldb.SCOPE_BASE,attrs=['userAccountControl'])[0]['userAccountControl'][0])
 replace(v['dn'],'userAccountControl',[str(flags|65536)])
reset=sid('svc_portal_reset');group=sid('svc_portal_group')
for right in ['(OA;CI;CR;00299570-246d-11d0-a768-00aa006e0529;;'+reset+')']+['(OA;CI;WP;'+guid(a)+';;'+reset+')' for a in ['pwdLastSet','lockoutTime']]:
 ace('OU=PortalA,'+people,right)
ace(users['pprotected'],'(OD;;CR;00299570-246d-11d0-a768-00aa006e0529;;'+reset+')')
groups={}
for name in ['PortalAllowed','PortalOther','PortalOperators','PortalScope','PortalNested','PortalPrivileged']:
 if not db.search(base=base,expression='(sAMAccountName='+name+')'): db.newgroup(name,groupou='OU=Access,OU=Groups')
 dn='CN='+name+','+ga;groups[name]=dn
 raw=db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['objectGUID'])[0]['objectGUID'][0]
 replace(dn,'mS-DS-ConsistencyGuid',[raw]);replace(dn,'anasIdentityAnchor',[str(uuid.UUID(bytes_le=raw))])
for dn in groups.values():replace(dn,'member',[])
ace(groups['PortalAllowed'],'(OA;;WP;'+guid('member')+';;'+group+')')
replace(groups['PortalOperators'],'member',[users['poperator']])
replace(groups['PortalScope'],'member',[users[x] for x in ['pnormal','pfirst','pexpired','pscopea','pprotected','pcasuser']])
replace(groups['PortalNested'],'member',[users['pscopea']])
replace(groups['PortalAllowed'],'member',[groups['PortalNested']])
replace(groups['PortalPrivileged'],'member',[])
for name, members in [('FS Share RW',['pnormal','pcasuser','pscopea']),('Admins',['poperator','pprotected'])]:
 row=db.search(base=base,expression='(&(objectClass=group)(cn='+name+'))',attrs=['member'])[0]
 existing=[str(v) for v in row.get('member',[])]
 replace(str(row.dn),'member',list(dict.fromkeys(existing+[users[x] for x in members])))
# A nested editable candidate that would confer Admins is protected by ancestry.
admin_group=db.search(base=base,expression='(&(objectClass=group)(cn=Admins))',attrs=['member'])[0]
replace(str(admin_group.dn),'member',list(dict.fromkeys([str(v) for v in admin_group.get('member',[])]+[groups['PortalPrivileged']])))
replace(users['pfirst'],'pwdLastSet',['0'])
# Fixture policy permits an immediate old-password change while retaining history/complexity.
replace(base,'minPwdAge',['0']);replace(base,'pwdHistoryLength',['5'])
anchors={name:str(db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['anasIdentityAnchor'])[0]['anasIdentityAnchor'][0]) for name,dn in (users|groups).items()}
cfg={'anchors':anchors,'host':os.environ['SAMBA_DC_HOST_IP'],'server_name':os.environ['SAMBA_DC_DC_DOMAIN'],'base':base,'users':users,'groups':groups,'services':services,'old':pw,'new':newpw,'wrong':'Incorrect-'+uuid.uuid4().hex+'!aA9','admin_dn':os.environ['SAMBA_DC_ADMIN_DN']}
open('/tmp/portal-fixture.json','w').write(json.dumps(cfg));os.chmod('/tmp/portal-fixture.json',0o600)
print(json.dumps({'fixture':'created','users':len(users),'dedicated_services':2,'acl_guid_fields':['member','pwdLastSet','lockoutTime']}))
