#!/usr/bin/env python3
# TEST_CASES: MAIL-T-003
"""Finance-only native mail migration proof, before activating the new worker.

Uses the tagged portal fixture and cached candidate image. Credentials, raw LDAP
logs and snapshots remain in the same-host private directory. No existing user
password or unrelated container is modified.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

root = Path(os.environ['PORTAL_MAIL_ROOT'])
assert re.fullmatch(r'/home/whl/anas-portal-admin-[A-Za-z0-9-]+', str(root))
os.umask(0o077)
import time
run_id = time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())
seed = json.loads((root / 'private/seed.private.json').read_text())
refs = json.loads((root / 'private/refs.private.json').read_text())
artifacts = json.loads((root / 'evidence/candidate-artifacts.json').read_text())
assert re.fullmatch(r'pua[0-9a-f]{8}', seed['prefix'])

def info(name):
    return json.loads(subprocess.check_output(['docker', 'inspect', name]))[0]

worker = info('anas_finance_samba_dc_anchor')
assert worker['Image'] == artifacts['samba_dc_anchor']['previous_image'], 'run before the mail worker is activated'
env = dict(item.split('=', 1) for item in worker['Config']['Env'])
env.update(ANCHOR_MAIL_DOMAIN='finance.hlong.wang', ANCHOR_MAIL_USER_BASE=refs['users']['normal']['dn'].split(',', 1)[1], ANCHOR_MAIL_SEARCH_BASE=seed['base'])
assert env['ANCHOR_MAIL_USER_BASE'].lower().endswith(',ou=people,' + seed['base'].lower())
assert all('\n' not in value for value in env.values())
envfile = root / 'private/mail-native.env'
envfile.write_text(''.join(key + '=' + value + '\n' for key, value in env.items()))

child = r'''
import dataclasses, hashlib, json, sys
import ldap
from anchor_worker import Directory, Settings
from mail_policy import desired_mail
f=json.load(sys.stdin); d=Directory(Settings.from_env())
attrs=['objectGUID','mS-DS-ConsistencyGuid',d.settings.anchor_attribute,'sAMAccountName','userPrincipalName','givenName','sn','displayName','memberOf','primaryGroupID','userAccountControl','pwdLastSet','accountExpires','lockoutTime']
def snapshot():
 rows=d.connect().search_s(d.settings.mail_user_base,ldap.SCOPE_SUBTREE,'(&(objectClass=user)(!(objectClass=computer)))',attrs)
 return sorted((dn,sorted((k,sorted(v)) for k,v in a.items())) for dn,a in rows if dn)
def digest(value):return hashlib.sha256(repr(value).encode()).hexdigest()
before=snapshot(); checks={}
if f['phase']=='default':
 n=d.reconcile_mail(); checks['default_generated']=n==6
elif f['phase']=='conflict':
 d.settings=dataclasses.replace(d.settings,mail_domain='hlong.wang')
 try:d.reconcile_mail();checks['conflict_rejected']=False
 except ValueError:checks['conflict_rejected']=True
 rows=list(d.iter_entries(d.settings.mail_user_base,'(&(objectClass=user)(!(objectClass=computer)))'))
 checks['conflict_no_partial_writes']=all(a.get('mail')==[desired_mail(a,'finance.hlong.wang')] for dn,a in rows)
elif f['phase']=='migration':
 d.settings=dataclasses.replace(d.settings,mail_domain='hlong.wang'); checks['suffix_migration']=d.reconcile_mail()==6
 rows=list(d.iter_entries(d.settings.mail_user_base,'(&(objectClass=user)(!(objectClass=computer)))'))
 checks['all_existing_addresses_updated']=all(a.get('mail')==[desired_mail(a,'hlong.wang')] for dn,a in rows)
 checks['idempotent_scan']=d.reconcile_mail()==0
 d.settings=dataclasses.replace(d.settings,mail_domain='finance.hlong.wang');checks['default_suffix_restored']=d.reconcile_mail()==6
elif f['phase']=='denied':
 denied=dataclasses.replace(d.settings,bind_dn=f['readonly_dn'],bind_password=f['readonly_password'],mail_domain='hlong.wang')
 probe=Directory(denied)
 try:probe.reconcile_mail();checks['write_denial_not_success']=False
 except ldap.INSUFFICIENT_ACCESS:checks['write_denial_not_success']=True
 probe.close()
 m=[(ldap.MOD_REPLACE,'mail',[b'forbidden@example.invalid'])]
 try:d.connect().modify_s(f['outside_dn'],m);checks['outside_mail_acl_denied']=False
 except ldap.INSUFFICIENT_ACCESS:checks['outside_mail_acl_denied']=True
if f['phase'] in ('default','migration'):
 for dn,a in d.iter_entries(d.settings.mail_user_base,'(&(objectClass=user)(!(objectClass=computer)))'):
  checks['address_matches_rule']=checks.get('address_matches_rule',True) and a.get('mail')==[desired_mail(a,'finance.hlong.wang')]
checks['other_identity_profile_password_group_fields_unchanged']=before==snapshot()
checks['outside_person_not_managed']=not d.mail_in_scope(f['outside_dn'])
print(json.dumps({'phase':f['phase'],'checks':checks,'personnel_fields_sha256':digest(before)}));d.close()
assert all(checks.values())
'''
# Root directory access is confined to the tagged out-of-scope fixture's mail.
control = r'''
import json,os,sys,uuid,base64
import ldb
from samba.auth import system_session
from samba.param import LoadParm
from samba.samdb import SamDB
f=json.load(sys.stdin);lp=LoadParm();lp.load('/etc/samba/smb.conf');db=SamDB(url='/var/lib/samba/private/sam.ldb',session_info=system_session(),lp=lp)
rows=db.search(base=os.environ['SAMBA_DC_BASE_DN'],expression='(objectGUID='+ldb.binary_encode(uuid.UUID(f['guid']).bytes_le)+')',attrs=['description','mail'])
assert len(rows)==1 and str(rows[0].get('description',[''])[0])=='ANAS portal acceptance '+f['prefix']
if f['action']=='read':print(json.dumps({'mail':[base64.b64encode(v).decode() for v in rows[0].get('mail',[])]}))
else:
 values=[base64.b64decode(v) for v in f['mail']];m=ldb.Message();m.dn=rows[0].dn;m['mail']=ldb.MessageElement(values,ldb.FLAG_MOD_REPLACE,'mail');db.modify(m);print('{}')
'''

def outside(action, mail=None):
    data = {'guid': refs['users']['outside']['guid'], 'prefix': seed['prefix'], 'action': action, 'mail': mail or []}
    result = subprocess.run(['docker', 'exec', '-i', 'anas_finance_samba_dc', 'python3', '-c', control], input=json.dumps(data).encode(), capture_output=True)
    (root / ('private/mail-control-' + action + '-' + run_id + '.log')).write_bytes(result.stderr)
    assert result.returncode == 0, 'owned fixture mail control failed'
    return json.loads(result.stdout)

results = []
def phase(name):
    portal = info('anas_finance_user_portal')
    pe = dict(item.split('=', 1) for item in portal['Config']['Env'])
    data = {'phase': name, 'outside_dn': refs['users']['outside']['dn'], 'readonly_dn': pe['USER_PORTAL_LDAP_BIND_DN'], 'readonly_password': pe['USER_PORTAL_LDAP_BIND_PASSWORD']}
    args = ['docker', 'run', '--rm', '-i', '--network', 'host', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--read-only', '--env-file', str(envfile), '--workdir', '/app', '--entrypoint', 'python3']
    for mount in worker['Mounts']:
        if mount['Destination'] == '/certs':
            args += ['--mount', 'type=bind,src=' + mount['Source'] + ',dst=/certs,readonly']
    # The original worker already uses the DC resolver; retain its DNS config.
    for ip in worker['HostConfig'].get('Dns', []):
        args += ['--dns', ip]
    args += [artifacts['samba_dc_anchor']['image'], '-c', child]
    result = subprocess.run(args, input=json.dumps(data).encode(), capture_output=True)
    (root / ('private/mail-native-' + name + '-' + run_id + '.log')).write_bytes(result.stderr)
    if result.stdout:
        report = json.loads(result.stdout);results.append(report)
        (root / 'evidence/mail-native.json').write_text(json.dumps({'phases': results}, indent=2))
    assert result.returncode == 0, name + ' failed; private diagnostic retained'

saved = outside('read')['mail']
try:
    phase('default')
    import base64
    outside('write', [base64.b64encode((seed['prefix'] + 'normal@hlong.wang').encode()).decode()])
    phase('conflict')
    outside('write', saved)
    phase('migration')
    phase('denied')
finally:
    outside('write', saved)
    envfile.unlink(missing_ok=True)
print(json.dumps({'phases': len(results), 'checks': sum(len(item['checks']) for item in results), 'all_passed': all(all(item['checks'].values()) for item in results)}))
