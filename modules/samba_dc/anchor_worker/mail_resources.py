"""Primary address coordination in the approved AD mailbox/claim namespace.

The backend supplies bounded LDAP reads, atomic Add and compare-and-modify.
No password, protocol identity or downstream readiness is managed here.
"""
from __future__ import annotations

import hashlib
import json
import uuid

from mail_policy import default_address, desired_mail, values


FIELDS = ('objectClass', 'cn', 'description', 'anasMailboxID', 'anasMailOwnerAnchor',
          'anasMailboxKind', 'anasMailDefaultAddress', 'anasMailAddress',
          'anasMailAddressKind', 'anasMailState', 'anasMailConfigVersion',
          'anasMailOperationID', 'anasMailOperationState', 'anasMailOperationData', 'anasMailQuotaState')


def one(attrs, name):
    found = values(attrs, name)
    if len(found) != 1:
        raise ValueError('mail primary required singleton missing')
    return found[0].decode('utf-8')


def canonical_uuid(value):
    if str(uuid.UUID(value)) != value:
        raise ValueError('mail primary noncanonical UUID')
    return value


def address(value):
    local, domain = value.split('@')
    if default_address(local, domain).decode() != value:
        raise ValueError('mail primary noncanonical address')
    return value


def locator(base, *, owner=None, email=None):
    if owner is not None:
        key = 'anas-primary-mailbox\n' + canonical_uuid(owner)
        ou = 'Mailboxes'
    else:
        key, ou = address(email), 'Addresses'
    return 'CN=' + hashlib.sha256(key.encode()).hexdigest() + ',OU=' + ou + ',OU=Mail,' + base


def data(attrs):
    raw = one(attrs, 'anasMailOperationData')
    if len(raw) > 65536:
        raise ValueError('mail primary operation too large')
    result = json.loads(raw)
    if not isinstance(result, dict):
        raise ValueError('mail primary operation unknown')
    return result


def encoded(**fields):
    return {key: [value.encode()] for key, value in fields.items()}


class PrimaryCoordinator:
    def __init__(self, backend, base, domain):
        self.db, self.base, self.domain = backend, base, domain

    def resource(self, row, kind, owner, box_id=None):
        if row is None:
            raise ValueError('mail primary resource missing')
        classes = {v.lower() for v in values(row, 'objectClass')}
        if kind.lower().encode() not in classes or b'user' in classes:
            raise ValueError('mail primary resource class conflict')
        for attr in ('anasMailboxID', 'anasMailOwnerAnchor', 'anasMailConfigVersion', 'anasMailOperationID'):
            canonical_uuid(one(row, attr))
        if one(row, 'anasMailOwnerAnchor') != owner or box_id and one(row, 'anasMailboxID') != box_id:
            raise ValueError('mail primary resource ownership conflict')
        return row

    def cas(self, dn, before, **changes):
        changes['anasMailConfigVersion'] = str(uuid.uuid4())
        self.db.cas(dn, before, encoded(**changes))
        current = self.db.read(dn)
        if current is None or any(values(current, key) != wanted for key, wanted in encoded(**changes).items()):
            raise RuntimeError('mail primary write result unknown; reconcile')
        return current

    def claim(self, email, owner, box_id):
        dn = locator(self.base, email=email)
        row = self.resource(self.db.read(dn), 'anasMailAddressClaim', owner, box_id)
        if one(row, 'anasMailAddress') != email:
            raise ValueError('mail primary claim address conflict')
        return dn, row

    def reserve(self, email, owner, box_id, operation):
        dn = locator(self.base, email=email)
        row = self.db.read(dn)
        if row is None:
            record = encoded(objectClass='anasMailAddressClaim', cn=dn[3:].split(',')[0],
                             anasMailboxID=box_id, anasMailOwnerAnchor=owner,
                             anasMailAddress=email, anasMailAddressKind='primary',
                             anasMailState='pending', anasMailConfigVersion=str(uuid.uuid4()),
                             anasMailOperationID=operation, anasMailOperationState='reserved')
            self.db.add(dn, record)
        dn, row = self.claim(email, owner, box_id)
        state, stage = one(row, 'anasMailState'), one(row, 'anasMailOperationState')
        if state == 'pending' and stage == 'reserved' and one(row, 'anasMailOperationID') == operation and one(row, 'anasMailAddressKind') == 'primary':
            return dn, row
        if state == 'active' and stage == 'complete' and one(row, 'anasMailAddressKind') in ('primary', 'alias'):
            if one(row, 'anasMailOperationID') == operation and one(row, 'anasMailAddressKind') == 'primary':
                return dn, row
            # Reusing one's retained active suffix alias is safe only under the
            # primary box's durable pending operation; never adopt retained or
            # disabled claims, another box, or somebody else's pending work.
            return dn, self.cas(dn, row, anasMailState='pending', anasMailOperationID=operation,
                                anasMailOperationState='reserved', anasMailAddressKind='primary')
        raise ValueError('mail primary claim has another operation or unavailable state')

    def person(self, guid, owner, target):
        result = self.db.person(guid)
        if result is None:
            raise ValueError('mail primary person absent')
        dn, row = result
        binary = values(row, 'mS-DS-ConsistencyGuid')
        if not self.db.in_scope(dn) or values(row, 'objectGUID') != [uuid.UUID(guid).bytes_le] or len(binary) != 1 or len(binary[0]) != 16 or str(uuid.UUID(bytes_le=binary[0])) != owner or one(row, 'anasIdentityAnchor') != owner or desired_mail(row, self.domain) != target.encode():
            raise ValueError('mail primary person identity or username changed')
        self.db.unique_person(owner, guid)
        return dn, row

    def reconcile(self, dn, attrs):
        target_bytes = desired_mail(attrs, self.domain)
        if target_bytes is None or not self.db.in_scope(dn):
            return False
        target = address(target_bytes.decode())
        guid_values = values(attrs, 'objectGUID')
        if len(guid_values) != 1 or len(guid_values[0]) != 16:
            raise ValueError('mail primary person GUID missing')
        guid = str(uuid.UUID(bytes_le=guid_values[0]))
        # Anchor stamping can have happened immediately before this call.
        fresh = self.db.person(guid)
        if fresh is None:
            raise ValueError('mail primary person absent')
        owner = canonical_uuid(one(fresh[1], 'anasIdentityAnchor'))
        dn, person = self.person(guid, owner, target)
        box_dn = locator(self.base, owner=owner)
        box = self.db.read(box_dn)
        self.db.namespace(target, guid)
        if box is None:
            # Import mismatching existing addresses explicitly before enabling
            # coordination. Never guess a deleted mailbox's former identity.
            if values(person, 'mail') not in ([], [target_bytes]):
                raise ValueError('mail primary explicit existing-address import required')
            op = {'type': 'system.primary', 'object_guid': guid, 'from': target, 'to': target}
            box = encoded(objectClass='anasMailbox', cn=box_dn[3:].split(',')[0],
                          anasMailboxID=str(uuid.uuid4()), anasMailOwnerAnchor=owner,
                          anasMailboxKind='primary', anasMailDefaultAddress=target,
                          anasMailState='pending', anasMailConfigVersion=str(uuid.uuid4()),
                          anasMailOperationID=str(uuid.uuid4()), anasMailOperationState='reserved',
                          anasMailOperationData=json.dumps(op, separators=(',', ':')))
            self.db.add(box_dn, box)
            box = self.db.read(box_dn)
        box = self.resource(box, 'anasMailbox', owner)
        if one(box, 'anasMailboxKind') != 'primary':
            raise ValueError('mail primary mailbox kind conflict')
        op = data(box)
        if op.get('object_guid') != guid or op.get('type') not in ('person.import', 'user.create', 'system.primary'):
            raise ValueError('mail primary unknown operation or replaced person')
        old = address(one(box, 'anasMailDefaultAddress'))
        state, stage = one(box, 'anasMailState'), one(box, 'anasMailOperationState')
        if (state, stage) == ('active', 'complete'):
            self.db.namespace(old, guid)
            _, old_claim = self.claim(old, owner, one(box, 'anasMailboxID'))
            if one(old_claim, 'anasMailState') != 'active' or one(old_claim, 'anasMailOperationState') != 'complete' or one(old_claim, 'anasMailAddressKind') != 'primary' or one(old_claim, 'anasMailOperationID') != one(box, 'anasMailOperationID') or values(person, 'mail') != [old.encode()]:
                raise ValueError('mail primary completed directory facts disagree')
            if old == target:
                return False
            quota = values(box, 'anasMailQuotaState')
            if quota:
                if len(quota) != 1 or len(quota[0]) > 65536:
                    raise ValueError('mail primary quota operation unknown')
                management = json.loads(quota[0])
                if not isinstance(management, dict) or management.get('schema') != 'anas.mail-management/v1' or management.get('person_guid') != guid:
                    raise ValueError('mail primary quota operation unknown')
                if management.get('pending') is not None:
                    raise ValueError('mail primary address operation pending')
            if old.split('@')[0] != target.split('@')[0]:
                raise ValueError('mail primary username rename not enabled')
            op = {'type': 'system.primary', 'object_guid': guid, 'from': old, 'to': target}
            box = self.cas(box_dn, box, anasMailState='pending', anasMailOperationState='reserved',
                           anasMailOperationID=str(uuid.uuid4()), anasMailOperationData=json.dumps(op, separators=(',', ':')))
        elif (state, stage) != ('pending', 'reserved') or op.get('type') != 'system.primary':
            raise ValueError('mail primary pending foreign operation')
        if set(op) != {'type', 'object_guid', 'from', 'to'} or op['to'] != target or old != op['from'] or old.split('@')[0] != target.split('@')[0]:
            raise ValueError('mail primary pending policy changed')
        box_id, operation = one(box, 'anasMailboxID'), one(box, 'anasMailOperationID')
        self.db.namespace(old, guid)
        claim_dn, claim = self.reserve(target, owner, box_id, operation)
        # Recheck the full domain after the winning Add, before touching people.
        self.db.namespace(target, guid)
        if old != target:
            old_dn, old_claim = self.claim(old, owner, box_id)
            if one(old_claim, 'anasMailState') != 'active' or one(old_claim, 'anasMailOperationState') != 'complete' or one(old_claim, 'anasMailAddressKind') not in ('primary', 'alias'):
                raise ValueError('mail primary former address unavailable')
        dn, person = self.person(guid, owner, target)
        if values(person, 'mail') not in ([], [old.encode()], [target_bytes]):
            raise ValueError('mail primary person address changed concurrently')
        proxies = list(values(person, 'proxyAddresses'))
        # Preserve every original value, including non-SMTP aliases. New values
        # use lowercase smtp: and never rewrite another box's From preference.
        for email in (old, target):
            if not any(p[:5].lower() == b'smtp:' and p[5:].lower() == email.encode() for p in proxies):
                proxies.append(b'smtp:' + email.encode())
        if values(person, 'mail') != [target_bytes] or set(proxies) != set(values(person, 'proxyAddresses')):
            self.db.update_person(dn, person, target_bytes, proxies)
        _, verified = self.person(guid, owner, target)
        if values(verified, 'mail') != [target_bytes] or set(values(verified, 'proxyAddresses')) != set(proxies):
            raise RuntimeError('mail primary personnel result unknown; reconcile')
        self.db.namespace(target, guid)
        if old != target and one(old_claim, 'anasMailAddressKind') != 'alias':
            previous = json.dumps({'type': 'system.primary.previous', 'object_guid': guid, 'from': old, 'to': target}, separators=(',', ':'))
            self.cas(old_dn, old_claim, anasMailAddressKind='alias', anasMailOperationData=previous)
        if one(claim, 'anasMailState') != 'active':
            self.cas(claim_dn, claim, anasMailState='active', anasMailOperationState='complete')
        self.cas(box_dn, box, anasMailDefaultAddress=target, anasMailState='active', anasMailOperationState='complete')
        return True
