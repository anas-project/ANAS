# TEST_CASES: LLNG-READY-T-002
"""Directory state changes for one disposable user in the isolated LLNG domain.

Run inside the explicitly isolated Samba container, never against a production DC.
Passwords stay in the fixture's root-readable JSON and are never printed.
"""
import json
import sys
import time
import uuid
import pathlib

import ldb
from samba.auth import system_session
from samba.param import LoadParm
from samba.samdb import SamDB
from samba.credentials import Credentials, DONT_USE_KERBEROS

lp = LoadParm()
lp.load('/var/lib/samba/etc/smb.conf')
sam = SamDB(url='/var/lib/samba/private/sam.ldb', session_info=system_session(), lp=lp)
assert str(sam.domain_dn()).lower() == 'dc=llngtest,dc=invalid', 'isolated test domain required'
action = sys.argv[1]
assert action in {'create', 'scope', 'normal', 'reset', 'expired', 'disabled', 'locked', 'account-expired', 'rename', 'delete'}
name = 'llngpasskey'
if action == 'create':
    with open('/fixture/secrets.json') as source:
        password = json.load(source)['old']
    rows = sam.search(base=sam.domain_dn(), expression='(sAMAccountName=llngpasskey)', attrs=['objectGUID'])
    assert not rows, 'fixture user already exists; do not overwrite credentials'
    sam.newuser(name, password, userou='OU=People')
rows = sam.search(base=sam.domain_dn(), expression='(|(sAMAccountName=llngpasskey)(sAMAccountName=llngpasskeyrenamed))', attrs=['objectGUID', 'sAMAccountName'])
assert len(rows) == 1
row = rows[0]
anchor = uuid.UUID(bytes_le=bytes(row['objectGUID'][0]))
if action in {'delete', 'scope'}:
    # Use real LDAPS so deletion reaches the audit producer and its consumers.
    credentials = Credentials()
    credentials.guess(lp)
    credentials.set_username('Administrator')
    credentials.set_password(json.loads(pathlib.Path('/fixture/secrets.json').read_text())['admin'])
    credentials.set_kerberos_state(DONT_USE_KERBEROS)
    lp.set('tls verify peer', 'ca_and_name')
    hosts = pathlib.Path('/etc/hosts')
    if 'dc.llngtest.invalid' not in hosts.read_text():
        with hosts.open('a') as handle:
            handle.write('\n127.0.0.1 dc.llngtest.invalid\n')
    remote = SamDB(url='ldaps://dc.llngtest.invalid:636', credentials=credentials, lp=lp)
    if action == 'delete':
        remote.delete(row.dn)
    else:
        target = 'CN=llngpasskey,OU=People,DC=llngtest,DC=invalid'
        if str(row.dn).lower() != target.lower():
            remote.rename(row.dn, ldb.Dn(remote, target))
    print(json.dumps({'action': action, 'fixture_user': name}))
    sys.exit(0)
values = {'userAccountControl': '512', 'lockoutTime': '0', 'accountExpires': '0'}
if action == 'create':
    values.update({'anasIdentityAnchor': str(anchor), 'mS-DS-ConsistencyGuid': anchor.bytes_le, 'mail': name+'@llngtest.invalid', 'displayName': 'Passkey test'})
elif action == 'reset':
    values['pwdLastSet'] = '0'
elif action == 'normal':
    values.update({'sAMAccountName': name, 'pwdLastSet': '-1'})
elif action == 'disabled':
    values['userAccountControl'] = '514'
elif action == 'locked':
    values['lockoutTime'] = str(int((time.time() + 11644473600) * 10000000))
elif action == 'account-expired':
    values['accountExpires'] = '1'
elif action == 'rename':
    values['sAMAccountName'] = name+'renamed'
elif action == 'expired':
    # Preserve real pwdLastSet and let the domain's actual age computation expire it.
    domain = ldb.Message()
    domain.dn = ldb.Dn(sam, sam.domain_dn())
    domain['maxPwdAge'] = ldb.MessageElement('-600000000', ldb.FLAG_MOD_REPLACE, 'maxPwdAge')
    sam.modify(domain)
if action == 'normal':
    domain = ldb.Message()
    domain.dn = ldb.Dn(sam, sam.domain_dn())
    domain['maxPwdAge'] = ldb.MessageElement('-36288000000000', ldb.FLAG_MOD_REPLACE, 'maxPwdAge')
    sam.modify(domain)
message = ldb.Message()
message.dn = row.dn
for key, value in values.items():
    message[key] = ldb.MessageElement(value, ldb.FLAG_MOD_REPLACE, key)
sam.modify(message)
if action == 'create':
    sam.add_remove_group_members('LLNG App A', [name], add_members_operation=True)
row = sam.search(base=message.dn, scope=ldb.SCOPE_BASE, attrs=['pwdLastSet', 'userAccountControl', 'msDS-User-Account-Control-Computed', 'accountExpires'])[0]
print(json.dumps({'action': action, 'flags': int(str(row['msDS-User-Account-Control-Computed'][0])), 'password_reset': str(row['pwdLastSet'][0]) == '0'}))
