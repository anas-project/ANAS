#!/usr/bin/env python3
# TEST_CASES: PORTAL-T-008, MAIL-T-005, GROUPCAT-T-002
"""Run the shipped Go ownership adapter in a disconnected, disposable AD forest.

Requires a locally available ANAS Samba image (including structure.sh), Docker,
OpenSSL and a Linux amd64 test
binary built with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -tags
portal_native -c ./modules/user_portal/portal`. No production directory inputs.
"""
import argparse
import hashlib
import json
import os
import pathlib
import secrets
import subprocess
import tempfile
import time
import uuid

PROVISION = r'''
import logging,os,pathlib
from samba.provision import provision
from samba.auth import system_session
pathlib.Path('/etc/samba/smb.conf').unlink(missing_ok=True)
provision(logging.getLogger('owner-test'),system_session(),smbconf='/etc/samba/smb.conf',realm='OWNER.TEST',domain='OWNER',hostname='proof',hostip='127.0.0.1',dns_backend='SAMBA_INTERNAL',dns_forwarder='',serverrole='active directory domain controller',adminpass=os.environ['OWNER_TEST_ADMIN_PASSWORD'],use_rfc2307=True,skip_sysvolacl=True)
p=pathlib.Path('/etc/samba/smb.conf');text=p.read_text();assert '[global]' in text
text=text.replace('[global]','[global]\n\ttls keyfile = /certs/key.pem\n\ttls certfile = /certs/cert.pem\n\ttls cafile = /certs/cert.pem\n\ttls enabled = yes\n\tinterfaces = lo 127.0.0.1\n\tbind interfaces only = yes',1);p.write_text(text)

import os
os.execvp('samba',['samba','-F','--no-process-group','--debug-stdout','--debuglevel=0'])
'''


PREPARE = r'''
import json,os,uuid,secrets,ldb
from samba.auth import system_session
from samba.param import LoadParm
from samba.samdb import SamDB
from samba.credentials import Credentials,DONT_USE_KERBEROS
from samba.dcerpc import security,misc
from samba.ndr import ndr_pack,ndr_unpack
lp=LoadParm();lp.load('/etc/samba/smb.conf');lp.set('tls verify peer','ca_and_name')
db=SamDB(url='/var/lib/samba/private/sam.ldb',session_info=system_session(),lp=lp)
base=str(db.domain_dn());assert base=='DC=owner,DC=test';ou='OU=People,'+base;db.create_ou(ou)
pw='Aa1!'+secrets.token_urlsafe(24);db.newuser('creator',pw)
creator=db.search(base=base,expression='(sAMAccountName=creator)',attrs=['objectSid'])[0];sid=str(ndr_unpack(security.dom_sid,creator['objectSid'][0]))
user_class=str(ndr_unpack(misc.GUID,db.search(base=str(db.get_schema_basedn()),expression='(lDAPDisplayName=user)',attrs=['schemaIDGUID'])[0]['schemaIDGUID'][0]))
sd=ndr_unpack(security.descriptor,db.search(base=ou,scope=ldb.SCOPE_BASE,attrs=['nTSecurityDescriptor'],controls=['sd_flags:1:4'])[0]['nTSecurityDescriptor'][0]);sd.dacl_add(security.descriptor.from_sddl('D:(OA;CI;CC;'+user_class+';;'+sid+')',security.dom_sid(db.get_domain_sid())).dacl.aces[0])
m=ldb.Message();m.dn=ldb.Dn(db,ou);m['nTSecurityDescriptor']=ldb.MessageElement([ndr_pack(sd)],ldb.FLAG_MOD_REPLACE,'nTSecurityDescriptor');db.modify(m,controls=['sd_flags:1:4'])
def identity(dn):
 row=db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['objectGUID','objectSid','description'])[0]
 return {'dn':str(row.dn),'anchor':str(row['description'][0]),'guid':str(ndr_unpack(misc.GUID,row['objectGUID'][0])),'sid':str(ndr_unpack(security.dom_sid,row['objectSid'][0]))}
def anchor(dn):
 m=ldb.Message();m.dn=ldb.Dn(db,dn);m['description']=ldb.MessageElement([str(uuid.uuid4())],ldb.FLAG_MOD_REPLACE,'description');db.modify(m);return identity(dn)
actor=anchor('CN=Administrator,CN=Users,'+base);creator=anchor(str(creator.dn))
creds=Credentials();creds.guess(lp);creds.set_bind_dn(creator['dn']);creds.set_password(pw);creds.set_kerberos_state(DONT_USE_KERBEROS)
client=SamDB(url='ldaps://proof.owner.test',credentials=creds,lp=lp)
targets=[]
for name in ['success','partial','denied']:
 dn='CN='+name+','+ou;client.add({'dn':dn,'objectClass':['top','person','organizationalPerson','user'],'cn':name,'sAMAccountName':name,'description':str(uuid.uuid4()),'userAccountControl':'512','unicodePwd':('"Aa1!'+secrets.token_urlsafe(24)+'"').encode('utf-16-le')});targets.append(identity(dn))
group=identity(anchor('CN=Domain Admins,CN=Users,'+base)['dn']);group['domain_risk']=True
# A concurrently removed group exercises a real failed second LDAP write.
db.newgroup('removed');deleted=anchor('CN=removed,CN=Users,'+base);db.delete(deleted['dn']);deleted['domain_risk']=True
fixture={'config':{'LDAPURL':'ldaps://proof.owner.test','BaseDN':base,'UsersDN':ou,'AnchorAttribute':'description'},'actor':actor,'creator':creator,'password':os.environ['OWNER_TEST_ADMIN_PASSWORD'],'creator_password':pw,'targets':targets,'group':group,'deleted_group':deleted}
open('/run/owner.private.json','w').write(json.dumps(fixture));os.chmod('/run/owner.private.json',0o600)
'''


POST = r'''
exec(open('/run/owner-prepare.py').read().split("base=str(db.domain_dn())")[0])
f=json.load(open('/run/owner.private.json'));base=f['config']['BaseDN'];creds=Credentials();creds.guess(lp);creds.set_bind_dn(f['creator']['dn']);creds.set_password(f['creator_password']);creds.set_kerberos_state(DONT_USE_KERBEROS);client=SamDB(url='ldaps://proof.owner.test',credentials=creds,lp=lp)
checks={};das=db.get_domain_sid()+'-512'
for i,label in [(0,'promoted'),(1,'partial')]:
 dn=f['targets'][i]['dn'];row=db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['nTSecurityDescriptor','memberOf'],controls=['sd_flags:1:5'])[0];sd=ndr_unpack(security.descriptor,row['nTSecurityDescriptor'][0]);checks[label+'_owner_is_domain_admins']=str(sd.owner_sid)==das
 checks[label+'_membership_correct']=any(str(x).startswith('CN=Domain Admins,') for x in (row['memberOf'] if 'memberOf' in row else []))==(i==0)
 sd.dacl_add(security.descriptor.from_sddl('D:(A;;RP;;;'+f['creator']['sid']+')',security.dom_sid(db.get_domain_sid())).dacl.aces[0]);m=ldb.Message();m.dn=ldb.Dn(db,dn);m['nTSecurityDescriptor']=ldb.MessageElement([ndr_pack(sd)],ldb.FLAG_MOD_REPLACE,'nTSecurityDescriptor')
 try:client.modify(m,controls=['sd_flags:1:4']);checks[label+'_creator_cannot_write_dacl']=False
 except ldb.LdbError as e:checks[label+'_creator_cannot_write_dacl']=e.args[0]==ldb.ERR_INSUFFICIENT_ACCESS_RIGHTS
last=db.search(base=f['targets'][2]['dn'],scope=ldb.SCOPE_BASE,attrs=['memberOf'])[0];checks['denied_target_not_promoted']=not any(str(x).startswith('CN=Domain Admins,') for x in (last['memberOf'] if 'memberOf' in last else []))

import pathlib

# Run the exact production bootstrap branch with real samba-tool commands.
# Only this disconnected fake forest receives the resulting administrator roles.
import subprocess
source = pathlib.Path('/usr/local/bin/structure.sh').read_text()
start = source.index('# deal with admin\n')
end = source.index('\n# ', start + 1)
bootstrap = source[:source.index('# waiting for samba startup')] + source[start:end]
if not db.search(base=base, expression='(sAMAccountName=Admins)', attrs=['cn']):
    db.newgroup('Admins')
for name, mail_domain in [('moduleadmin', 'owner.test'), ('moduleadmin2', 'hlong.wang')]:
    expected = name + '@' + mail_domain
    environment = os.environ.copy()
    environment.update({'SAMBA_DC_ADMIN_NAME': name,
                        'SAMBA_DC_ADMIN_PASSWORD': 'Aa1!' + secrets.token_urlsafe(24),
                        'SAMBA_DC_ADMIN_DN': 'CN=' + name + ',OU=People,' + base,
                        'SAMBA_DC_BASE_USERS_DN': 'OU=People,' + base,
                        'SAMBA_DC_BASE_USERS_DN_PREFIX': 'OU=People',
                        'SAMBA_DC_ADMIN_MAIL': expected,
                        'SAMBA_DC_ADMIN_DISPLAY_NAME': 'ModuleAdmin',
                        'SAMBA_DC_CREATE_STRUCTURE': 'false'})
    first = subprocess.run(['bash', '-c', bootstrap], env=environment, capture_output=True)
    checks[name + '_production_bootstrap_succeeded'] = first.returncode == 0
    row = client.search(base='CN=' + name + ',OU=People,' + base, scope=ldb.SCOPE_BASE, attrs=['mail'])[0]
    checks[name + '_mail_available_via_ldaps_immediately'] = str(row['mail'][0]) == expected
    environment['SAMBA_DC_ADMIN_MAIL'] = name + '@must-not-replace.test'
    second = subprocess.run(['bash', '-c', bootstrap], env=environment, capture_output=True)
    row = client.search(base='CN=' + name + ',OU=People,' + base, scope=ldb.SCOPE_BASE, attrs=['mail'])[0]
    checks[name + '_existing_bootstrap_does_not_overwrite_mail'] = second.returncode == 0 and str(row['mail'][0]) == expected

print(json.dumps({'checks':checks}));assert all(checks.values()),'owner boundary failed'
'''



GROUP_PROOF = r'''
import importlib.util,json,ldb
from samba.auth import system_session
from samba.param import LoadParm
from samba.samdb import SamDB
lp=LoadParm();lp.load_default();db=SamDB(url='/var/lib/samba/private/sam.ldb',session_info=system_session(),lp=lp)
spec=importlib.util.spec_from_file_location('identity_groups','/usr/local/bin/identity_groups.py');module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
catalog=json.load(open('/certs/catalog.json'));base=str(db.domain_dn())
for g in catalog['groups']:
 if g['managed']:
  prefix,sep,oldbase=g['dn'].rpartition(',OU=Groups,');assert sep and oldbase.startswith('DC=');g['dn']=prefix+',OU=Groups,'+base
builtins=db.search(base=base,expression='(objectClass=group)',attrs=['objectGUID','member','description'])
def snapshot(rows):return {str(r.dn):{k:[bytes(v).hex() for v in r.get(k,[])] for k in ['objectGUID','member','description']} for r in rows}
before=snapshot(builtins)
module.reconcile(db,catalog)
created={}
for g in catalog['groups']:
 if not g['managed']:continue
 row=db.search(base=g['dn'],scope=ldb.SCOPE_BASE,attrs=['objectGUID','description','member'])[0]
 assert module.text(row['description'][0])==g['description']['en']
 assert 'member' not in row
 created[g['dn']]=row['objectGUID'][0].hex()
# A stale managed description is updated without recreating the object.
first=next(g for g in catalog['groups'] if g['managed']);m=ldb.Message();m.dn=ldb.Dn(db,first['dn']);m['description']=ldb.MessageElement('stale description',ldb.FLAG_MOD_REPLACE,'description');db.modify(m)
module.reconcile(db,catalog)
checks={'catalog_creates_declared_groups_and_descriptions':len(created)>0,'catalog_rerun_preserves_group_guids':True,'catalog_does_not_change_builtin_groups':True,'catalog_repairs_managed_description':True}
for dn,guid in created.items():
 row=db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['objectGUID','description','member'])[0];checks['catalog_rerun_preserves_group_guids'] &= row['objectGUID'][0].hex()==guid
checks['catalog_repairs_managed_description']=module.text(db.search(base=first['dn'],scope=ldb.SCOPE_BASE,attrs=['description'])[0]['description'][0])==first['description']['en']
after=snapshot(db.search(base=base,expression='(objectClass=group)',attrs=['objectGUID','member','description']))
checks['catalog_does_not_change_builtin_groups']=all(after[dn]==state for dn,state in before.items())
invalid=json.loads(json.dumps(catalog));invalid['groups'][0]['dn']='CN=Outside,CN=Users,'+base
try:module.reconcile(db,invalid);checks['catalog_rejects_outside_groups_directory']=False
except ValueError:checks['catalog_rejects_outside_groups_directory']=True
print(json.dumps({'checks':checks,'managed_groups':len(created)}));assert all(checks.values())
'''

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True)
    parser.add_argument('--group-catalog',type=pathlib.Path)
    parser.add_argument('--native-binary', required=True, type=pathlib.Path)
    parser.add_argument('--evidence-dir', required=True, type=pathlib.Path)
    args = parser.parse_args()
    os.umask(0o077)
    root = args.evidence_dir.resolve()
    root.mkdir(parents=True, exist_ok=True)
    private = pathlib.Path(tempfile.mkdtemp(prefix='owner-private-', dir=root))
    name = 'anas-portal-owner-' + uuid.uuid4().hex[:12]
    label = 'anas.acceptance=' + name
    password = 'Test-Aa1!' + secrets.token_urlsafe(24)
    envfile = private / 'realm.env'
    envfile.write_text('OWNER_TEST_ADMIN_PASSWORD=' + password + '\n')
    report = {'realm': 'owner.test', 'network': 'none', 'published_ports': False,
              'production_directory_modified': False, 'sysvol_acl_not_tested': True,
              'native_binary_sha256': hashlib.sha256(args.native_binary.read_bytes()).hexdigest()}
    def run(command, log=None, check=True):
        result = subprocess.run(command, capture_output=True)
        if log:
            (private / log).write_bytes(result.stdout + result.stderr)
        if check and result.returncode:
            raise RuntimeError('isolated test failed; inspect private/' + (log or 'diagnostics'))
        return result
    try:
        run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
             '-keyout', str(private / 'key.pem'), '-out', str(private / 'cert.pem'),
             '-subj', '/CN=proof.owner.test', '-addext', 'subjectAltName=DNS:proof.owner.test',
             '-days', '1'], 'certificate.log')
        if args.group_catalog:
            (private/'catalog.json').write_bytes(args.group_catalog.read_bytes())
        report['image'] = json.loads(run(['docker', 'image', 'inspect', args.image]).stdout)[0]['Id']
        run(['docker', 'run', '-d', '--name', name, '--label', label, '--network', 'none',
             '--hostname', 'proof.owner.test', '--add-host', 'proof.owner.test:127.0.0.1',
             '--memory', '768m', '--cpus', '1', '--pids-limit', '160',
             '--env-file', str(envfile), '--mount', 'type=bind,src=' + str(private) + ',dst=/certs,readonly',
             '--entrypoint', 'python3', args.image, '-c', PROVISION], 'create.log')
        ready = False
        for _ in range(90):
            result = run(['docker', 'exec', name, 'python3', '-c',
                          'import socket; s=socket.create_connection(("127.0.0.1",636),1); s.close()'], check=False)
            if result.returncode == 0:
                ready = True
                break
            state = json.loads(run(['docker', 'inspect', name]).stdout)[0]
            if not state['State']['Running']:
                break
            time.sleep(1)
        if not ready:
            raise RuntimeError('isolated LDAP did not start')
        if args.group_catalog:
            group_result=run(['docker','exec',name,'python3','-c',GROUP_PROOF],'group-proof.log')
            report['group_catalog']=json.loads(group_result.stdout.splitlines()[-1])
        run(['docker', 'cp', str(args.native_binary.resolve()), name + ':/run/owner-native.test'], 'binary.log')
        run(['docker', 'exec', name, 'chmod', '700', '/run/owner-native.test'])
        # post-validation reuses the imports/connection prefix of this file.
        prep = private / 'owner-prepare.py'
        prep.write_text(PREPARE)
        run(['docker', 'cp', str(prep), name + ':/run/owner-prepare.py'])
        run(['docker', 'exec', name, 'python3', '/run/owner-prepare.py'], 'prepare.log')
        result = run(['docker', 'exec', '-e', 'PORTAL_OWNER_FIXTURE=/run/owner.private.json',
                      name, '/run/owner-native.test', '-test.run', '^TestNativeOwnerTransfer$', '-test.v'],
                     'go.log', check=False)
        go_result = run(['docker', 'exec', name, 'cat', '/run/owner-go-results.json'], 'go-result.log', check=False)
        report['adapter'] = json.loads(go_result.stdout) if go_result.returncode == 0 else {}
        if result.returncode:
            raise RuntimeError('native adapter failed; inspect private/go.log')
        result = run(['docker', 'exec', name, 'python3', '-c', POST], 'post.log')
        report['directory'] = json.loads(result.stdout)
        if not all(report['adapter']['checks'].values()) or not all(report['directory']['checks'].values()):
            raise RuntimeError('ownership checks failed')
        report['passed'] = True
    finally:
        state = run(['docker', 'inspect', name], check=False)
        if state.returncode == 0:
            state = json.loads(state.stdout)[0]
            if state['Config']['Labels'].get('anas.acceptance') != name:
                raise RuntimeError('refusing to clean an unrelated container')
            run(['docker', 'logs', name], 'container.log', check=False)
            run(['docker', 'rm', '-fv', state['Id']], 'cleanup.log')
        report['container_removed'] = run(['docker', 'inspect', name], check=False).returncode != 0
        envfile.unlink(missing_ok=True)
        (private / 'key.pem').unlink(missing_ok=True)
        (root / 'owner-isolated-results.json').write_text(json.dumps(report, indent=2))
    print(json.dumps(report))

if __name__ == '__main__':
    main()
