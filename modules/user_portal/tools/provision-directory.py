#!/usr/bin/env python3
# TEST_CASES: PORTAL-T-007
"""Operator-run Samba setup. Reads private JSON on stdin; emits no credentials.

Run inside the selected Samba DC, never from the portal service. Creates only
owned service accounts and object/attribute ACLs. It does not grant domain roles.
"""
import json
import os
import sys
import uuid
import ldb
from samba.auth import system_session
from samba.dcerpc import security, misc
from samba.ndr import ndr_pack, ndr_unpack
from samba.param import LoadParm
from samba.samdb import SamDB

owned = 'ANAS user portal scoped writer'
input_data = json.load(sys.stdin)
assert input_data.get('confirm') == 'provision-user-portal-scoped-writers'
lp = LoadParm()
lp.load('/etc/samba/smb.conf')
db = SamDB(url='/var/lib/samba/private/sam.ldb', session_info=system_session(), lp=lp)
base = str(db.domain_dn())
assert base.lower() == os.environ['SAMBA_DC_BASE_DN'].lower()
assert input_data['base_dn'].lower() == base.lower()
schema = str(db.get_schema_basedn())
anchor_attr = os.environ['SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE']
people_base = os.environ['SAMBA_DC_BASE_USERS_DN']
group_base = os.environ['SAMBA_DC_BASE_GROUPS_DN']
service_base = os.environ['SAMBA_DC_BASE_SERVICE_ACCOUNTS_DN']

def under(dn, parent):
    x, y = ldb.Dn(db, dn), ldb.Dn(db, parent)
    return x == y or x.is_child_of(y)

def replace(dn, attr, values):
    message = ldb.Message()
    message.dn = ldb.Dn(db, dn)
    message[attr] = ldb.MessageElement(values, ldb.FLAG_MOD_REPLACE, attr)
    db.modify(message)

def attribute_guid(name):
    rows = db.search(base=schema, expression='(lDAPDisplayName=' + ldb.binary_encode(name) + ')', attrs=['schemaIDGUID'])
    assert len(rows) == 1
    return str(ndr_unpack(misc.GUID, rows[0]['schemaIDGUID'][0]))

acl_changes = 0

def ace(dn, text, remove=False):
    global acl_changes
    row = db.search(base=dn, scope=ldb.SCOPE_BASE, attrs=['nTSecurityDescriptor'], controls=['sd_flags:1:4'])[0]
    descriptor = ndr_unpack(security.descriptor, row['nTSecurityDescriptor'][0])
    wanted = security.descriptor.from_sddl('D:' + text, security.dom_sid(db.get_domain_sid())).dacl.aces[0]
    present = any(ndr_pack(old) == ndr_pack(wanted) for old in descriptor.dacl.aces)
    if present == (not remove):
        return
    if remove:
        original = descriptor.as_sddl(security.dom_sid(db.get_domain_sid()))
        assert text in original, 'cannot remove an owned ACE without an exact match'
        descriptor = security.descriptor.from_sddl(original.replace(text, ''), security.dom_sid(db.get_domain_sid()))
    else:
        descriptor.dacl_add(wanted)
    message = ldb.Message()
    message.dn = ldb.Dn(db, dn)
    message['nTSecurityDescriptor'] = ldb.MessageElement([ndr_pack(descriptor)], ldb.FLAG_MOD_REPLACE, 'nTSecurityDescriptor')
    db.modify(message, controls=['sd_flags:1:4'])
    acl_changes += 1

def validate_writer(row):
    assert under(str(row.dn), service_base)
    assert str(row.get('description', [''])[0]) == owned
    assert not row.get('memberOf'), 'service writer must not hold additional group roles'
    # AD does not include the primary group in memberOf. In particular a
    # primary Domain Admins membership must not pass this service-account check.
    assert str(row['primaryGroupID'][0]) == '513', 'service writer must use Domain Users as its primary group'
    flags = int(str(row['userAccountControl'][0]))
    assert flags & 0x200 and not flags & 0x3800, 'ordinary user service account required'


services = {}
service_dns = []
created = []
for kind, expected_name in [('profile', 'svc_portal_profile'), ('reset', 'svc_portal_reset'), ('group', 'svc_portal_group'), ('create', 'svc_portal_create')]:
    password = input_data['passwords'][kind]
    assert isinstance(password, str) and 24 <= len(password) <= 256
    attrs = ['objectSid', 'description', 'memberOf', 'primaryGroupID', 'userAccountControl']
    rows = db.search(base=base, expression='(sAMAccountName=' + expected_name + ')', attrs=attrs)
    if not rows:
        db.newuser(expected_name, password, userou='OU=Service Accounts', description=owned)
        created.append(expected_name)
        rows = db.search(base=base, expression='(sAMAccountName=' + expected_name + ')', attrs=attrs)
    assert len(rows) == 1
    validate_writer(rows[0])
    service_dns.append(str(rows[0].dn))
    services[kind] = str(ndr_unpack(security.dom_sid, rows[0]['objectSid'][0]))
    account = db.search(base=str(rows[0].dn), scope=ldb.SCOPE_BASE, attrs=['userAccountControl'])[0]
    flags = int(str(account['userAccountControl'][0]))
    if not flags & 65536:
        replace(str(rows[0].dn), 'userAccountControl', [str(flags | 65536)])
    # A rerun does not reset any existing password or expand its group roles.

groups = db.search(base=base, expression='(objectClass=group)', attrs=['objectSid', 'cn', 'member', 'objectGUID'])
by_dn = {str(g.dn).lower(): g for g in groups}
by_sid = {str(ndr_unpack(security.dom_sid, g['objectSid'][0])): str(g.dn) for g in groups}
privileged_rids = {'512', '516', '518', '519', '520', '521', '526', '527', '544', '548', '549', '550', '551', '552'}

def privileged(dn):
    seen, pending = set(), [dn.lower()]
    while pending:
        current = pending.pop()
        if current in seen:
            continue
        seen.add(current)
        row = by_dn.get(current)
        if row is not None:
            sid = str(ndr_unpack(security.dom_sid, row['objectSid'][0]))
            if sid.rsplit('-', 1)[-1] in privileged_rids:
                return True
        for parent in groups:
            if any(str(member).lower() == current for member in parent.get('member', [])):
                pending.append(str(parent.dn).lower())
    return False

# Check the primary group's effective ancestry too, before granting any ACLs.
domain_users_sid = str(db.get_domain_sid()) + '-513'
assert domain_users_sid in by_sid and not privileged(by_sid[domain_users_sid]), 'service primary group must not grant domain roles'
assert all(not privileged(dn) for dn in service_dns), 'service writer must not inherit domain roles'

# Only user objects can be created; the application fixes their DN below this
# OU and their ordinary account attributes. This grants no group/computer/OU
# creation, existing-password reset, or domain role.
user_schema = db.search(base=schema, expression='(&(objectClass=classSchema)(lDAPDisplayName=user))', attrs=['schemaIDGUID'])
assert len(user_schema) == 1
user_class_guid = str(ndr_unpack(misc.GUID, user_schema[0]['schemaIDGUID'][0]))
ace(people_base, '(OA;CI;CC;' + user_class_guid + ';;' + services['create'] + ')')

registered = set(filter(None, os.environ['SAMBA_DC_APPLICATION_CATALOG_GROUPS'].split(',')))
# The provider's older registry omitted this explicitly created infrastructure
# group. It is classified as privileged and NEVER delegated to the group writer.
registered.add('Unix Admins')
member_guid = attribute_guid('member')
for group in groups:
    name = str(group['cn'][0])
    if under(str(group.dn), group_base) and name in registered and not privileged(str(group.dn)):
        ace(str(group.dn), '(OA;;WP;' + member_guid + ';;' + services['group'] + ')')

profile_guids = [attribute_guid(attr) for attr in ['givenName', 'sn', 'displayName']]
reset_guid = '00299570-246d-11d0-a768-00aa006e0529'
reset_properties = [attribute_guid(attr) for attr in ['pwdLastSet', 'lockoutTime']]
anchor_writer = db.search(base=os.environ['SAMBA_DC_ANCHOR_BIND_DN'], scope=ldb.SCOPE_BASE, attrs=['objectSid'])[0]
anchor_sid = str(ndr_unpack(security.dom_sid, anchor_writer['objectSid'][0]))
mail_guid = attribute_guid('mail')
ace(people_base, '(OA;CI;WP;' + mail_guid + ';;' + anchor_sid + ')')
# New ordinary people inherit attribute-scoped rights. Protected people below
# receive explicit denials for password/reset attributes, not a full-control ACE.
for guid in profile_guids:
    ace(people_base, '(OA;CI;WP;' + guid + ';;' + services['profile'] + ')')
ace(people_base, '(OA;CI;CR;' + reset_guid + ';;' + services['reset'] + ')')
for guid in reset_properties:
    ace(people_base, '(OA;CI;WP;' + guid + ';;' + services['reset'] + ')')

anchored = 0
protected = 0
primary_protected = 0
people = db.search(base=base, expression='(&(objectClass=user)(!(objectClass=computer)))', attrs=['objectGUID', 'objectSid', 'mS-DS-ConsistencyGuid', anchor_attr, 'userAccountControl', 'primaryGroupID'])
for person in people:
    dn = str(person.dn)
    if not under(dn, people_base):
        # Remove only this tool's prior explicit ordinary-write allows. Older
        # versions delegated human accounts outside the now-confirmed OU.
        for guid in profile_guids:
            ace(dn, '(OA;;WP;' + guid + ';;' + services['profile'] + ')', remove=True)
        for access, guid in [('CR', reset_guid)] + [('WP', guid) for guid in reset_properties]:
            ace(dn, '(OA;;' + access + ';' + guid + ';;' + services['reset'] + ')', remove=True)
        continue
    sid = str(ndr_unpack(security.dom_sid, person['objectSid'][0]))
    if under(dn, service_base) or sid.rsplit('-', 1)[-1] in {'501', '502'} or int(str(person['userAccountControl'][0])) & 0x3800:
        continue
    ace(dn, '(OA;;WP;' + mail_guid + ';;' + anchor_sid + ')')
    binary = person.get('mS-DS-ConsistencyGuid', person['objectGUID'])[0]
    expected = str(uuid.UUID(bytes_le=bytes(binary)))
    if person.get(anchor_attr):
        assert str(person[anchor_attr][0]).lower() == expected
    else:
        replace(dn, anchor_attr, [expected])
        anchored += 1
    if not person.get('mS-DS-ConsistencyGuid'):
        replace(dn, 'mS-DS-ConsistencyGuid', [binary])
    for guid in profile_guids:
        ace(dn, '(OA;;WP;' + guid + ';;' + services['profile'] + ')')
    primary_sid = sid.rsplit('-', 1)[0] + '-' + str(person['primaryGroupID'][0])
    assert primary_sid in by_sid, 'primary group unavailable; refusing password delegation'
    primary_risk = privileged(by_sid[primary_sid])
    risk = sid.endswith('-500') or privileged(dn) or primary_risk
    primary_protected += int(primary_risk)
    for access, guid in [('CR', reset_guid)] + [('WP', guid) for guid in reset_properties]:
        ace(dn, '(' + ('OD' if risk else 'OA') + ';;' + access + ';' + guid + ';;' + services['reset'] + ')')
    protected += int(risk)
print(json.dumps({'created_services': created, 'anchored_personnel': anchored, 'protected_personnel': protected, 'primary_group_protected_personnel': primary_protected, 'acl_changes': acl_changes, 'domain_role_grants': 0}))
