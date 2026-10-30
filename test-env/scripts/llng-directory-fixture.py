# TEST_CASES: LLNG-READY-T-006
# TEST_CASES: LLNG-READY-T-002
# TEST_CASES: LLNG-READY-T-001
"""Mutate only marked portal acceptance identities in the disposable LLNG AD."""
import json
import pathlib
import sys
import time
import uuid
import ldb
from samba.auth import system_session
from samba.param import LoadParm
from samba.samdb import SamDB
from samba.credentials import Credentials, DONT_USE_KERBEROS

lp = LoadParm()
lp.load('/var/lib/samba/etc/smb.conf')
db = SamDB(url='/var/lib/samba/private/sam.ldb', session_info=system_session(), lp=lp)
assert str(db.domain_dn()).lower() == 'dc=llngtest,dc=invalid'
fixture = json.loads(pathlib.Path('/var/lib/samba/llng-portal-fixture.json').read_text())
action, name = sys.argv[1:3]
assert name in {'portalnormal', 'portalother', 'portalmanager', 'portaltarget'}
assert action in {'disable', 'enable', 'remove_portal', 'add_portal', 'remove_a', 'add_a',
                  'add_admin', 'remove_admin',
                  'nest_portal', 'unnest_portal', 'remove_nested', 'add_nested',
                  'locked', 'unlocked', 'restore_profile', 'restore_password',
                  'force_change', 'password_current', 'short_password_age', 'normal_password_age', 'state'}
identity = fixture['users'][name]
rows = db.search(base=db.domain_dn(), expression='(objectGUID=' + ldb.binary_encode(uuid.UUID(identity['anchor']).bytes_le) + ')', attrs=['description', 'userAccountControl'])
assert len(rows) == 1 and str(rows[0].get('description', [''])[0]) == 'ANAS LLNG readiness 20261023'
user = rows[0]
change = ldb.Message()
if action in {'restore_password', 'state'}:
    pass
elif action in {'short_password_age', 'normal_password_age'}:
    change.dn = ldb.Dn(db, db.domain_dn())
    change['maxPwdAge'] = ldb.MessageElement('-600000000' if action == 'short_password_age' else '-36288000000000', ldb.FLAG_MOD_REPLACE, 'maxPwdAge')
elif action in {'force_change', 'password_current'}:
    change.dn = user.dn
    change['pwdLastSet'] = ldb.MessageElement('0' if action == 'force_change' else '-1', ldb.FLAG_MOD_REPLACE, 'pwdLastSet')
elif action == 'restore_profile':
    change.dn = user.dn
    for attr, values in {'givenName': [], 'sn': [], 'displayName': [name]}.items():
        change[attr] = ldb.MessageElement(values, ldb.FLAG_MOD_REPLACE, attr)
elif action in {'locked', 'unlocked'}:
    change.dn = user.dn
    value = str(int((time.time() + 11644473600) * 10000000)) if action == 'locked' else '0'
    change['lockoutTime'] = ldb.MessageElement(value, ldb.FLAG_MOD_REPLACE, 'lockoutTime')
elif action in {'disable', 'enable'}:
    change.dn = user.dn
    flags = int(str(user['userAccountControl'][0]))
    flags = flags | 2 if action == 'disable' else flags & ~2
    change['userAccountControl'] = ldb.MessageElement([str(flags)], ldb.FLAG_MOD_REPLACE, 'userAccountControl')
else:
    group_name = ('Admins' if action.endswith('admin') else 'LLNG Portal' if action.endswith('portal')
                  else 'LLNG Nested' if action.endswith('nested') else 'LLNG App A')
    group = fixture['groups'][group_name]
    target = db.search(base=group['dn'], scope=ldb.SCOPE_BASE, attrs=['anasIdentityAnchor', 'description', 'member'])[0]
    assert str(target['anasIdentityAnchor'][0]) == group['anchor']
    assert str(target.get('description', [''])[0]) == 'ANAS LLNG readiness 20261023'
    members = [str(value) for value in target.get('member', []) if str(value).lower() != str(user.dn).lower()]
    if action in {'nest_portal', 'unnest_portal'}:
        nested = fixture['groups']['LLNG Nested']['dn']
        members = [value for value in members if value.lower() != nested.lower()]
        members.append(nested if action == 'nest_portal' else str(user.dn))
    elif action.startswith('add_'):
        members.append(str(user.dn))
    change.dn = target.dn
    change['member'] = ldb.MessageElement(members, ldb.FLAG_MOD_REPLACE, 'member')
# A local sam.ldb write bypasses the running DC's audit stream. Apply through
# authenticated LDAPS so acceptance covers the real producer, not a forged event.
credentials = Credentials()
credentials.guess(lp)
credentials.set_username('Administrator')
credentials.set_password(json.loads(pathlib.Path('/fixture/secrets.json').read_text())['admin'])
credentials.set_kerberos_state(DONT_USE_KERBEROS)
# The disposable CA has no CRL. Retain CA, lifetime and hostname validation.
lp.set('tls verify peer', 'ca_and_name')
# Docker regenerates this disposable host-network container's hosts file on
# restart. Resolve the test DC locally while still verifying its DNS identity.
hosts = pathlib.Path('/etc/hosts')
if 'dc.llngtest.invalid' not in hosts.read_text():
    with hosts.open('a') as handle:
        handle.write('\n127.0.0.1 dc.llngtest.invalid\n')
remote = SamDB(url='ldaps://dc.llngtest.invalid:636', credentials=credentials, lp=lp)
if action == 'restore_password':
    remote.setpassword('(objectGUID=' + ldb.binary_encode(uuid.UUID(identity['anchor']).bytes_le) + ')',
                       json.loads(pathlib.Path('/fixture/secrets.json').read_text())['old'])
elif action != 'state':
    remote.modify(change)
state = remote.search(base=user.dn, scope=ldb.SCOPE_BASE, attrs=['msDS-User-Account-Control-Computed', 'pwdLastSet'])[0]
print(json.dumps({'action': action, 'fixture_user': name, 'flags': int(str(state['msDS-User-Account-Control-Computed'][0])), 'password_reset': str(state['pwdLastSet'][0]) == '0'}))
