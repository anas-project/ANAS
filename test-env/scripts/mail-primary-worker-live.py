#!/usr/bin/python3
"""Actual Anchor Worker/python-ldap path, only in MAILSCHEMA.INVALID."""
import concurrent.futures
import json
from pathlib import Path
from types import SimpleNamespace
import sys
import threading
import uuid

sys.path.insert(0, '/candidate/modules/samba_dc/anchor_worker')
import ldap
from anchor_worker import Directory
from mail_resource_ldap import LDAPResources
from mail_resources import PrimaryCoordinator, encoded, locator, one

BASE = 'DC=mailschema,DC=invalid'
PEOPLE = 'OU=People,' + BASE
SECRETS = json.loads(Path('/input/secrets.json').read_text())
RESULTS = []


def record(name, **details):
    RESULTS.append(dict(check=name, passed=True, **details))
    print(json.dumps(RESULTS[-1]), flush=True)


def directory(user='primary', domain='mailschema.invalid'):
    dn = 'CN=' + user + ',OU=Service Accounts,' + BASE if user != 'Administrator' else 'CN=Administrator,CN=Users,' + BASE
    return Directory(SimpleNamespace(ldap_url='ldaps://127.0.0.1:636', bind_dn=dn,
        bind_password=SECRETS[user], tls_cacert='/test-ca.pem', page_size=100,
        binary_anchor_attribute='mS-DS-ConsistencyGuid', anchor_attribute='anasIdentityAnchor',
        mail_domain=domain, mail_user_base=PEOPLE, mail_search_base=BASE))


def attrs(connection, dn):
    rows = connection.search_s(dn, ldap.SCOPE_BASE, '(objectClass=*)', ['*'])
    assert len(rows) == 1
    return rows[0][1]


def person(admin, name):
    dn, anchor = 'CN=' + name + ',' + PEOPLE, uuid.uuid4()
    admin.add_s(dn, list(encoded(objectClass='user', cn=name, sAMAccountName=name,
        mail=name+'@mailschema.invalid', userAccountControl='514',
        anasIdentityAnchor=str(anchor)).items()) + [('mS-DS-ConsistencyGuid', [anchor.bytes_le])])
    return dn, str(anchor)


def run():
    admin_directory = directory('Administrator')
    admin = admin_directory.connect()
    root = admin.search_s('', ldap.SCOPE_BASE, '(objectClass=*)', ['defaultNamingContext', 'supportedControl'])[0][1]
    assert root['defaultNamingContext'] == [BASE.encode()], 'refusing non-fixture domain'
    service = directory()
    backend = LDAPResources(service)
    assert backend.installed()
    coordinator = service.mail_resources()
    assert coordinator is not None
    alice = 'CN=alice,' + PEOPLE
    owner = one(attrs(admin, alice), 'anasIdentityAnchor')
    box_dn = locator(BASE, owner=owner)
    initial = backend.read(box_dn)
    box_id = one(initial, 'anasMailboxID')
    original_person = attrs(admin, alice)
    assert not service.ensure_mail(alice)
    writes = directory(domain='relay.invalid')
    assert writes.ensure_mail(alice)
    assert one(backend.read(box_dn), 'anasMailboxID') == box_id
    assert one(backend.read(locator(BASE, email='alice@mailschema.invalid')), 'anasMailAddressKind') == 'alias'
    for name in ('objectGUID', 'objectSid', 'mS-DS-ConsistencyGuid', 'anasIdentityAnchor', 'sAMAccountName', 'pwdLastSet', 'userAccountControl'):
        assert attrs(admin, alice).get(name) == original_person.get(name), name
    assert set(original_person.get('proxyAddresses', [])) <= set(attrs(admin, alice)['proxyAddresses'])
    assert not writes.ensure_mail(alice)
    assert service.ensure_mail(alice)
    assert one(backend.read(box_dn), 'anasMailboxID') == box_id
    record('system_suffix_same_box_old_address_alias_and_return', assertion_advertised=b'1.3.6.1.1.12' in root['supportedControl'])
    # The exact runtime adapter must reject removed/replaced proxy preimages
    # atomically. A simultaneous unrelated profile edit needs no write lock.
    snapshot = service.read_target(alice)[1]
    concurrent_proxies = [b'smtp:concurrent-only@mailschema.invalid']
    admin.modify_s(alice, [(ldap.MOD_REPLACE, 'proxyAddresses', concurrent_proxies)])
    try: backend.update_person(alice, snapshot, b'forged@mailschema.invalid', [b'smtp:forged@mailschema.invalid'])
    except ldap.LDAPError: pass
    else: raise AssertionError('stale personnel comparison succeeded')
    assert one(attrs(admin, alice), 'mail') == 'alice@mailschema.invalid'
    assert attrs(admin, alice)['proxyAddresses'] == concurrent_proxies
    admin.modify_s(alice, [(ldap.MOD_REPLACE, 'proxyAddresses', snapshot['proxyAddresses'])])
    record('stale_person_preimage_rejected_without_mail_or_proxy_overwrite')
    def denied(fn):
        try: fn()
        except ldap.INSUFFICIENT_ACCESS: return
        raise AssertionError('primary service exceeded fixed rights')
    denied(lambda: service.connect().modify_s(alice, [(ldap.MOD_REPLACE, 'sAMAccountName', [b'forged'])]))
    denied(lambda: service.connect().modify_s(box_dn, [(ldap.MOD_REPLACE, 'anasMailboxID', [str(uuid.uuid4()).encode()])]))
    denied(lambda: service.connect().modify_s(box_dn, [(ldap.MOD_REPLACE, 'anasMailOwnerAnchor', [str(uuid.uuid4()).encode()])]))
    denied(lambda: service.connect().delete_s(box_dn))
    denied(lambda: service.connect().modify_s('CN=reader,OU=Service Accounts,'+BASE, [(ldap.MOD_REPLACE, 'mail', [b'forged@mailschema.invalid'])]))
    record('primary_service_scope_and_immutable_identity_denied', operations=5)
    cas_dn = 'CN=worker-cas,OU=Mailboxes,OU=Mail,' + BASE
    cas_row = encoded(objectClass='anasMailbox', cn='worker-cas',
        anasMailboxID=str(uuid.uuid4()), anasMailOwnerAnchor=owner, anasMailboxKind='additional',
        anasMailDefaultAddress='worker-cas@mailschema.invalid', anasMailState='pending',
        anasMailConfigVersion=str(uuid.uuid4()), anasMailOperationID=str(uuid.uuid4()), anasMailOperationState='reserved')
    backend.add(cas_dn, cas_row)
    def compare(index):
        bound = directory()
        try:
            LDAPResources(bound).cas(cas_dn, cas_row, encoded(anasMailConfigVersion=str(uuid.uuid4()), anasMailOperationState='candidate-'+str(index)))
            return True
        except (ldap.NO_SUCH_ATTRIBUTE, ldap.TYPE_OR_VALUE_EXISTS): return False
        finally: bound.close()
    with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
        assert sum(pool.map(compare, range(12))) == 1
    after = backend.read(cas_dn)
    assert one(after, 'anasMailConfigVersion') != one(cas_row, 'anasMailConfigVersion')
    assert one(after, 'anasMailOperationState').startswith('candidate-')
    record('runtime_resource_compare_one_winner', contenders=12, winners=1)
    # Concurrent proxy additions survive the strict Delete/Add; the caller's
    # read-back is then unknown and its durable pending operation can resume.
    side_dn, side_owner = person(admin, 'sideproxy')
    assert service.ensure_mail(side_dn)
    side = directory(domain='side.invalid'); adapter = LDAPResources(side)
    update = adapter.update_person
    def concurrent_proxy(dn, before, email, proxies):
        admin.modify_s(dn, [(ldap.MOD_ADD, 'proxyAddresses', [b'smtp:side-extra@mailschema.invalid'])])
        return update(dn, before, email, proxies)
    adapter.update_person = concurrent_proxy
    try: PrimaryCoordinator(adapter, BASE, 'side.invalid').reconcile(side_dn, side.read_target(side_dn)[1])
    except RuntimeError as exc: assert 'result unknown' in str(exc)
    else: raise AssertionError('concurrent proxy change was not surfaced')
    assert one(backend.read(locator(BASE, owner=side_owner)), 'anasMailState') == 'pending'
    assert b'smtp:side-extra@mailschema.invalid' in attrs(admin, side_dn)['proxyAddresses']
    assert side.ensure_mail(side_dn)
    assert b'smtp:side-extra@mailschema.invalid' in attrs(admin, side_dn)['proxyAddresses']
    side.close()
    record('concurrent_proxy_add_preserved_and_pending_resumed')
    # Every write, including the final commit, is interrupted before execution
    # and after a successful LDAP response to model a lost caller response.
    cases = 0
    for suffix, count in ((False, 5), (True, 6)):
        for lost in (False, True):
            for step in range(1, count + 1):
                cases += 1
                dn, person_owner = person(admin, 'fault' + str(cases))
                current = directory()
                if suffix: assert current.ensure_mail(dn)
                target_domain = 'fault.invalid' if suffix else 'mailschema.invalid'
                current.settings.mail_domain = target_domain
                adapter = LDAPResources(current)
                original = {name:getattr(adapter,name) for name in ('add', 'cas', 'update_person')}
                counter = [0]
                def wrap(name):
                    def call(*args, **kwargs):
                        counter[0] += 1
                        fail = counter[0] == step
                        if fail and not lost: raise OSError('injected before write')
                        result = original[name](*args, **kwargs)
                        if fail: raise OSError('injected lost response')
                        return result
                    return call
                for name in original: setattr(adapter, name, wrap(name))
                candidate = PrimaryCoordinator(adapter, BASE, target_domain)
                row = current.read_target(dn)[1]
                try: candidate.reconcile(dn, row)
                except OSError: pass
                else: raise AssertionError('write interruption not reached')
                existing = backend.read(locator(BASE, owner=person_owner))
                prior_id = one(existing, 'anasMailboxID') if existing else None
                current.ensure_mail(dn)
                final = backend.read(locator(BASE, owner=person_owner))
                if prior_id: assert one(final, 'anasMailboxID') == prior_id
                assert one(final, 'anasMailState') == 'active'
                assert not current.ensure_mail(dn)
                current.close()
    record('all_initial_and_suffix_write_boundaries_recover', cases=cases)
    # One actual system writer races eleven address-writer bindings on the same
    # deterministic claim DN. This exercises the shared reservation primitive;
    # the full alias HTTP workflow is still separate unfinished work.
    dn, race_owner = person(admin, 'crossrace')
    race = directory(domain='race.invalid')
    # First register the native address before changing the configured suffix.
    initial_service = directory(); assert initial_service.ensure_mail(dn); initial_service.close()
    gate = threading.Barrier(12)
    target = 'crossrace@race.invalid'; target_dn = locator(BASE, email=target)
    adapter = LDAPResources(race)
    add = adapter.add
    def system_add(dn, row):
        if dn == target_dn: gate.wait(timeout=30)
        return add(dn, row)
    adapter.add = system_add
    def contender(index):
        if index == 0:
            try:
                PrimaryCoordinator(adapter, BASE, 'race.invalid').reconcile(dn, race.read_target(dn)[1])
                return 'system'
            except ValueError: return None
        foreign = directory('address'); client = foreign.connect()
        record = encoded(objectClass='anasMailAddressClaim', cn=target_dn[3:].split(',')[0],
            anasMailboxID=box_id, anasMailOwnerAnchor=owner, anasMailAddress=target,
            anasMailAddressKind='alias', anasMailState='pending',
            anasMailOperationID=str(uuid.uuid4()), anasMailConfigVersion=str(uuid.uuid4()), anasMailOperationState='reserved')
        gate.wait(timeout=30)
        try: client.add_s(target_dn, list(record.items())); return 'address'
        except ldap.ALREADY_EXISTS: return None
        finally: foreign.close()
    with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
        winners = [item for item in pool.map(contender, range(12)) if item]
    assert len(winners) == 1, winners
    winning = backend.read(target_dn)
    assert one(winning, 'anasMailOwnerAnchor') == (race_owner if winners == ['system'] else owner)
    if winners == ['address']:
        assert one(attrs(admin, dn), 'mail') == 'crossrace@mailschema.invalid'
        try: race.ensure_mail(dn)
        except ValueError: pass
        else: raise AssertionError('system adopted foreign claim on retry')
    record('system_and_address_writers_one_claim_winner', contenders=12, winners=len(winners), winner=winners[0])
    # Installing schema cannot silently fall back to old mail-only writes when
    # a required managed OU is hidden or absent.
    from unittest.mock import patch
    backend.read = lambda _dn: None
    with patch('mail_resource_ldap.LDAPResources', return_value=backend):
        try: service.mail_resources()
        except ValueError as exc: assert 'preparation required' in str(exc)
        else: raise AssertionError('missing managed OU fell back to mail-only writes')
    record('installed_schema_missing_resource_ou_never_falls_back')
    service.close(); writes.close(); race.close(); admin_directory.close()


try:
    run()
finally:
    Path('/evidence/primary-worker-results.json').write_text(json.dumps(RESULTS, indent=2))
