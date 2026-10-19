#!/usr/bin/env python3
# TEST_CASES: MAIL-T-005
"""Run in the confirmed finance Samba test DC; all temporary objects are removed."""
import json, os, secrets, uuid, subprocess
import ldb
from samba.auth import system_session
from samba.credentials import Credentials, DONT_USE_KERBEROS
from samba.dcerpc import security, misc
from samba.ndr import ndr_pack, ndr_unpack
from samba.param import LoadParm
from samba.samdb import SamDB
lp=LoadParm(); lp.load('/etc/samba/smb.conf'); lp.set('tls verify peer','ca_and_name')
db=SamDB(url='/var/lib/samba/private/sam.ldb', session_info=system_session(),lp=lp)
base=str(db.domain_dn()); assert base.lower()=='dc=finance,dc=hlong,dc=wang'; people=os.environ['SAMBA_DC_BASE_USERS_DN']; services=os.environ['SAMBA_DC_BASE_SERVICE_ACCOUNTS_DN']
prefix='pm'+secrets.token_hex(4); ou='OU='+prefix+','+people; creator=prefix+'c'; name=prefix+'u'; creator_dn=None
result={'stage':'prepare','checks':{},'fixtures_removed':False}
created=[]
try:
 db.add({'dn':ou,'objectClass':'organizationalUnit','ou':prefix}); created.append(ou)
 cli_name=prefix+'a';cli_dn='CN='+cli_name+','+ou;cli_mail=cli_name+'@finance.hlong.wang'
 command=subprocess.run(['samba-tool','user','add',cli_name,'--random-password','--userou='+ou.removesuffix(','+base),'--mail-address='+cli_mail],capture_output=True)
 assert command.returncode==0, 'native bootstrap mail argument rejected'
 created.append(cli_dn)
 row=db.search(base=cli_dn,scope=ldb.SCOPE_BASE,attrs=['mail'])[0]
 result['checks']['native_bootstrap_mail_argument']=str(row['mail'][0])==cli_mail
 password='Aa1!'+secrets.token_urlsafe(24)
 db.newuser(creator,password,userou=services.removesuffix(','+base),description='ANAS primary mail temporary native proof '+prefix)
 row=db.search(base=services,expression='(sAMAccountName='+creator+')',attrs=['objectSid'])[0]; creator_dn=str(row.dn); created.append(creator_dn)
 sid=str(ndr_unpack(security.dom_sid,row['objectSid'][0])); schema=str(db.get_schema_basedn())
 guids={}
 for attr in ['user','mail']:
  row=db.search(base=schema,expression='(lDAPDisplayName='+attr+')',attrs=['schemaIDGUID'])[0]
  guids[attr]=str(ndr_unpack(misc.GUID,row['schemaIDGUID'][0]))
 result['mail_schema_guid']=guids['mail']
 row=db.search(base=ou,scope=ldb.SCOPE_BASE,attrs=['nTSecurityDescriptor'],controls=['sd_flags:1:4'])[0]
 sd=ndr_unpack(security.descriptor,row['nTSecurityDescriptor'][0]); ace=security.descriptor.from_sddl('D:(OA;CI;CC;'+guids['user']+';;'+sid+')',security.dom_sid(db.get_domain_sid())).dacl.aces[0]; sd.dacl_add(ace)
 m=ldb.Message();m.dn=ldb.Dn(db,ou);m['nTSecurityDescriptor']=ldb.MessageElement([ndr_pack(sd)],ldb.FLAG_MOD_REPLACE,'nTSecurityDescriptor');db.modify(m,controls=['sd_flags:1:4'])
 creds=Credentials();creds.guess(lp);creds.set_bind_dn(creator_dn);creds.set_password(password);creds.set_kerberos_state(DONT_USE_KERBEROS)
 result['stage']='bind'
 remote=SamDB(url='ldaps://'+os.environ['SAMBA_DC_DC_DOMAIN'],credentials=creds,lp=lp)
 result['stage']='single_add'
 binary=uuid.uuid4().bytes_le; anchor=str(uuid.UUID(bytes_le=binary)); user_dn='CN='+name+','+ou
 initial='Aa1!'+secrets.token_urlsafe(24)
 remote.add({'dn':user_dn,'objectClass':['top','person','organizationalPerson','user'],'cn':name,'sAMAccountName':name,'userPrincipalName':name+'@'+os.environ['SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN'],'displayName':'Portal Mail Native Proof','givenName':'Portal','sn':'Proof','mail':name+'@finance.hlong.wang','userAccountControl':'512','unicodePwd':('"'+initial+'"').encode('utf-16-le'),'mS-DS-ConsistencyGuid':binary,os.environ['SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE']:anchor,'pwdLastSet':'0'})
 created.append(user_dn)
 row=db.search(base=user_dn,scope=ldb.SCOPE_BASE,attrs=['mail','mS-DS-ConsistencyGuid',os.environ['SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE'],'pwdLastSet','userAccountControl','memberOf'])[0]
 result['checks']['single_add_with_cc_only']=True
 result['checks']['mail_and_anchor']=str(row['mail'][0])==name+'@finance.hlong.wang' and bytes(row['mS-DS-ConsistencyGuid'][0])==binary and str(row[os.environ['SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE']][0])==anchor
 result['checks']['ordinary_force_change']=str(row['pwdLastSet'][0])=='0' and int(str(row['userAccountControl'][0]))==512 and not row.get('memberOf')
 try:
  remote.add({'dn':'CN='+prefix+'g,'+ou,'objectClass':'group','sAMAccountName':prefix+'g'})
  created.append('CN='+prefix+'g,'+ou); result['checks']['group_creation_denied']=False
 except ldb.LdbError as error: result['checks']['group_creation_denied']=error.args[0]==50
 result['stage']='complete'
except Exception as error:
 result['error_type']=type(error).__name__
 if isinstance(error,ldb.LdbError):result['ldap_error_code']=error.args[0]
finally:
 failures=[]
 for dn in reversed(created):
  try: db.delete(dn)
  except Exception as error:failures.append(type(error).__name__)
 result['fixtures_removed']=not failures
 print(json.dumps(result))
