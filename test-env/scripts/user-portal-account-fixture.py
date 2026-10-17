#!/usr/bin/env python3
# TEST_CASES: PORTAL-T-007
"""Run inside the selected Samba DC; stdin carries same-host private input."""
import base64
import json
import os
import re
import sys
import time
import uuid
import ldb
from samba.auth import system_session
from samba.dcerpc import security, misc
from samba.ndr import ndr_unpack
from samba.param import LoadParm
from samba.samdb import SamDB

f = json.load(sys.stdin)
action = os.environ['PORTAL_ACCOUNT_ACTION']
assert action in ['prepare', 'cleanup', 'baseline', 'references', 'primary-boundary']
assert re.fullmatch(r'pua[0-9a-f]{8}', f['prefix'])
lp = LoadParm(); lp.load('/etc/samba/smb.conf')
db = SamDB(url='/var/lib/samba/private/sam.ldb', session_info=system_session(), lp=lp)
base = str(db.domain_dn())
assert base.lower() == f['base'].lower() == os.environ['SAMBA_DC_BASE_DN'].lower()
marker = 'ANAS portal acceptance ' + f['prefix']
anchor_attr = os.environ['SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE']

def replace(dn, attr, values):
    m = ldb.Message(); m.dn = ldb.Dn(db, dn)
    m[attr] = ldb.MessageElement(values, ldb.FLAG_MOD_REPLACE, attr); db.modify(m)

def identity(dn):
    row = db.search(base=dn, scope=ldb.SCOPE_BASE, attrs=['objectGUID', anchor_attr, 'sAMAccountName', 'description'])[0]
    return {'dn': str(row.dn), 'guid': str(ndr_unpack(misc.GUID, row['objectGUID'][0])), 'anchor': str(row.get(anchor_attr, [''])[0]), 'username': str(row.get('sAMAccountName', [''])[0])}

def anchored(dn):
    row = db.search(base=dn, scope=ldb.SCOPE_BASE, attrs=['objectGUID'])[0]
    raw = row['objectGUID'][0]; value = str(uuid.UUID(bytes_le=bytes(raw)))
    replace(dn, 'mS-DS-ConsistencyGuid', [raw]); replace(dn, anchor_attr, [value])
    return identity(dn)

def group(name):
    rows = db.search(base=base, expression='(&(objectClass=group)(cn=' + ldb.binary_encode(name) + '))', attrs=['cn'])
    assert len(rows) == 1
    return str(rows[0].dn)

def member(dn, person_dn):
    m = ldb.Message(); m.dn = ldb.Dn(db, dn)
    m['member'] = ldb.MessageElement([person_dn], ldb.FLAG_MOD_ADD, 'member'); db.modify(m)

if action == 'baseline':
    rows = db.search(base=base, expression='(|(&(objectClass=user)(!(objectClass=computer)))(objectClass=group)(objectClass=organizationalUnit))', attrs=['objectGUID', 'nTSecurityDescriptor', 'mS-DS-ConsistencyGuid', anchor_attr], controls=['sd_flags:1:4'])
    data = [{'dn': str(row.dn), 'attributes': {key: [base64.b64encode(value).decode() for value in row[key]] for key in ['objectGUID','nTSecurityDescriptor','mS-DS-ConsistencyGuid',anchor_attr] if key in row}} for row in rows]
    print(json.dumps(data)); sys.exit(0)
def primary_domain_admin(item):
    assert item['username'] == f['prefix'] + 'privileged'
    rows = db.search(base=base, expression='(objectGUID=' + ldb.binary_encode(uuid.UUID(item['guid']).bytes_le) + ')', attrs=['description'])
    assert len(rows) == 1 and str(rows[0].dn).lower() == item['dn'].lower()
    assert str(rows[0].get('description', [''])[0]) == marker
    domain_admins = group('Domain Admins')
    replace(item['dn'], 'primaryGroupID', ['512'])
    members = db.search(base=domain_admins, scope=ldb.SCOPE_BASE, attrs=['member'])[0].get('member', [])
    if any(str(value).lower() == item['dn'].lower() for value in members):
        m = ldb.Message(); m.dn = ldb.Dn(db, domain_admins)
        m['member'] = ldb.MessageElement([item['dn']], ldb.FLAG_MOD_DELETE, 'member'); db.modify(m)
    current = db.search(base=item['dn'], scope=ldb.SCOPE_BASE, attrs=['primaryGroupID'])[0]
    members = db.search(base=domain_admins, scope=ldb.SCOPE_BASE, attrs=['member'])[0].get('member', [])
    assert str(current['primaryGroupID'][0]) == '512'
    assert not any(str(value).lower() == item['dn'].lower() for value in members)

if action == 'primary-boundary':
    primary_domain_admin(f['primary_target'])
    print(json.dumps({'owned_primary_group': 512, 'explicit_domain_admin_member': False})); sys.exit(0)
if action == 'cleanup':
    removed = []
    for item in f['objects'][::-1]:
        rows = db.search(base=base, expression='(objectGUID=' + ldb.binary_encode(uuid.UUID(item['guid']).bytes_le) + ')', attrs=['objectGUID', 'description'])
        if not rows:
            continue
        assert len(rows) == 1 and str(rows[0].dn).lower() == item['dn'].lower()
        assert str(rows[0].get('description', [''])[0]) == marker
        db.delete(rows[0].dn); removed.append(item['guid'])
    print(json.dumps({'removed_fixture_objects': len(removed), 'owned_prefix': f['prefix']})); sys.exit(0)
if action == 'references':
    rows = db.search(base=base, expression='(description=' + ldb.binary_encode(marker) + ')', attrs=['objectGUID'])
    print(json.dumps({'objects': [identity(str(row.dn)) for row in rows]})); sys.exit(0)

# Every named test identity must be absent before this one-use preparation.
for alias in ['normal', 'manager', 'domain', 'target', 'privileged', 'other']:
    assert not db.search(base=base, expression='(sAMAccountName=' + f['prefix'] + alias + ')', attrs=['cn'])
objects = []
people_ou = 'OU=' + f['prefix'] + ',' + os.environ['SAMBA_DC_BASE_USERS_DN']
admin_parent = 'OU=Admins,' + base
assert db.search(base=admin_parent, scope=ldb.SCOPE_BASE, attrs=['cn'])
admin_ou = 'OU=' + f['prefix'] + ',' + admin_parent
for dn in [people_ou, admin_ou]:
    db.create_ou(dn); replace(dn, 'description', [marker]); objects.append(identity(dn))
users = {}
for alias in ['normal', 'manager', 'domain', 'target', 'privileged', 'other']:
    name = f['prefix'] + alias
    parent = admin_ou if alias == 'target' else people_ou
    relative = parent[:-(len(base) + 1)]
    db.newuser(name, f['passwords'][alias], userou=relative, description=marker)
    dn = 'CN=' + name + ',' + parent
    replace(dn, 'displayName', [name]); replace(dn, 'givenName', ['Portal']); replace(dn, 'sn', ['Acceptance'])
    replace(dn, 'userPrincipalName', [name + '@' + os.environ['SAMBA_DC_DOMAIN']])
    users[alias] = anchored(dn); objects.append(users[alias])
for alias in ['manager', 'domain', 'target', 'other']:
    member(os.environ['SAMBA_DC_ADMIN_GROUP_DN'], users[alias]['dn'])
for alias in ['domain', 'privileged']:
    member(group('Domain Admins'), users[alias]['dn'])
primary_domain_admin(users['privileged'])
for alias in ['normal', 'manager', 'domain', 'privileged', 'other']:
    member(group('APP_nextcloud' if alias == 'normal' else 'APP_all'), users[alias]['dn'])
# An APP_ prefix is deliberately insufficient to enter the managed registry.
extra_name = 'APP_' + f['prefix']
db.newgroup(extra_name, groupou='OU=Apps,OU=Groups', description=marker)
extra = anchored(group(extra_name)); objects.append(extra)
# An indirect route to Admins exercises self-protection without editing nesting
# through the product API. Cleanup deletes only this tagged temporary group.
nested_name = f['prefix'] + 'nested'
db.newgroup(nested_name, groupou='OU=Access,OU=Groups', description=marker)
nested = anchored(group(nested_name)); objects.append(nested)
member(nested['dn'], users['manager']['dn']); member(os.environ['SAMBA_DC_ADMIN_GROUP_DN'], nested['dn'])
# Direct per-user policy wins over group policy, without changing domain policy.
pso = 'CN=' + f['prefix'] + ',CN=Password Settings Container,CN=System,' + base
db.add({'dn':pso,'objectClass':['top','msDS-PasswordSettings'],'description':marker,'msDS-PasswordSettingsPrecedence':'1','msDS-PasswordReversibleEncryptionEnabled':'FALSE','msDS-PasswordHistoryLength':'5','msDS-PasswordComplexityEnabled':'TRUE','msDS-MinimumPasswordLength':'12','msDS-MinimumPasswordAge':'0','msDS-MaximumPasswordAge':'-25920000000000','msDS-LockoutThreshold':'10','msDS-LockoutObservationWindow':'-18000000000','msDS-LockoutDuration':'-18000000000','msDS-PSOAppliesTo':[p['dn'] for p in users.values()]})
objects.append(identity(pso))
replace(users['target']['dn'], 'lockoutTime', [str(int((time.time()+11644473600)*10000000))])
print(json.dumps({'prefix':f['prefix'],'base':base,'users':users,'groups':{'extra':extra,'nested':nested},'objects':objects,'pso':pso}))
