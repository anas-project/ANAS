import copy
import json
import threading
import unittest
import uuid
from types import SimpleNamespace
from unittest.mock import Mock, patch

from mail_resources import PrimaryCoordinator, encoded, locator, one


BASE = 'DC=example,DC=test'
DN = 'CN=alice,OU=People,' + BASE


class Backend:
    def __init__(self):
        anchor, guid = uuid.uuid4(), uuid.uuid4()
        self.owner, self.guid = str(anchor), str(guid)
        self.people = {DN: dict(encoded(objectClass='user', anasIdentityAnchor=self.owner,
                                       sAMAccountName='alice', mail='alice@example.test',
                                       userAccountControl='512', uSNChanged='1'),
                               objectGUID=[guid.bytes_le], **{'mS-DS-ConsistencyGuid': [anchor.bytes_le]})}
        self.rows, self.writes, self.fail_at, self.lost = {}, 0, None, False
        self.lock = threading.RLock()

    def read(self, dn):
        with self.lock:
            return copy.deepcopy(self.rows.get(dn))

    def mutation(self, fn):
        with self.lock:
            self.writes += 1
            if self.writes == self.fail_at and not self.lost:
                raise OSError('injected before write')
            fn()
            if self.writes == self.fail_at:
                raise OSError('injected lost response')

    def add(self, dn, row):
        def write():
            if dn not in self.rows:
                self.rows[dn] = copy.deepcopy(row)
        self.mutation(write)

    def cas(self, dn, before, changes):
        def write():
            if self.rows[dn]['anasMailConfigVersion'] != before['anasMailConfigVersion']:
                raise ValueError('stale version')
            self.rows[dn].update(copy.deepcopy(changes))
        self.mutation(write)

    def in_scope(self, dn): return dn.endswith(',OU=People,' + BASE)

    def person(self, guid):
        with self.lock:
            return next(((dn, copy.deepcopy(row)) for dn, row in self.people.items() if row['objectGUID'] == [uuid.UUID(guid).bytes_le]), None)

    def unique_person(self, owner, guid):
        rows = [row for row in self.people.values() if row.get('anasIdentityAnchor') == [owner.encode()]]
        if len(rows) != 1 or rows[0]['objectGUID'] != [uuid.UUID(guid).bytes_le]:
            raise ValueError('duplicate anchor')

    def namespace(self, email, guid):
        for row in self.people.values():
            addresses = row.get('mail', []) + [v.strip()[5:].strip() for v in row.get('proxyAddresses', []) if v.strip()[:5].lower() == b'smtp:']
            if email.encode() in [v.strip().lower() for v in addresses] and row['objectGUID'] != [uuid.UUID(guid).bytes_le]:
                raise ValueError('namespace conflict')

    def update_person(self, dn, before, email, proxies):
        def write():
            current = self.people[dn]
            if any(current.get(name) != before.get(name) for name in ('objectGUID', 'mS-DS-ConsistencyGuid', 'anasIdentityAnchor', 'mail')) or not set(before.get('proxyAddresses', [])) <= set(current.get('proxyAddresses', [])):
                raise ValueError('person assertion failed')
            concurrent = [v for v in current.get('proxyAddresses', []) if v not in before.get('proxyAddresses', [])]
            self.people[dn].update(mail=[email], proxyAddresses=proxies + concurrent,
                                   uSNChanged=[str(int(one(before, 'uSNChanged')) + 1).encode()])
        self.mutation(write)

    def run(self, domain='example.test'):
        return PrimaryCoordinator(self, BASE, domain).reconcile(DN, copy.deepcopy(self.people[DN]))


class PrimaryResourcesTests(unittest.TestCase):
    def test_personal_operation_lock_blocks_suffix_and_former_primary_is_protected(self):
        db=Backend();db.run();dn=locator(BASE,owner=db.owner)
        state={'schema':'anas.mail-management/v1','person_guid':db.guid,'pending':{'request':{'operation_id':str(uuid.uuid4())}}}
        db.rows[dn]['anasMailQuotaState']=[json.dumps(state).encode()]
        before=copy.deepcopy((db.rows,db.people))
        with self.assertRaisesRegex(ValueError,'operation pending'):db.run('other.test')
        self.assertEqual((db.rows,db.people),before)
        self.assertFalse(db.run())
        state['pending']=None;db.rows[dn]['anasMailQuotaState']=[json.dumps(state).encode()]
        db.run('other.test')
        previous=db.rows[locator(BASE,email='alice@example.test')]
        self.assertEqual(json.loads(previous['anasMailOperationData'][0])['type'],'system.primary.previous')
    def test_worker_uses_resources_even_for_unchanged_mail_and_full_scan(self):
        from anchor_worker import Directory
        db = Backend()
        worker = object.__new__(Directory)
        worker.settings = SimpleNamespace(mail_domain='example.test', mail_user_base='OU=People,'+BASE, mail_search_base=BASE)
        worker.mail_in_scope = db.in_scope
        coordinator = Mock(); coordinator.reconcile.return_value = True
        worker.mail_resources = lambda: coordinator
        self.assertTrue(worker.ensure_mail(DN, db.people[DN]))
        worker.iter_entries = lambda *_args: [(DN, db.people[DN])]
        self.assertEqual(worker.reconcile_mail(), 1)
        self.assertEqual(coordinator.reconcile.call_count, 2)

    def test_installed_schema_missing_or_foreign_ou_never_uses_legacy_path(self):
        from anchor_worker import Directory
        worker = object.__new__(Directory)
        worker.settings = SimpleNamespace(mail_domain='example.test', mail_search_base=BASE)
        backend = Mock()
        with patch('mail_resource_ldap.LDAPResources', return_value=backend):
            backend.installed.return_value = False
            self.assertIsNone(worker.mail_resources())
            backend.installed.return_value = True
            for row in (None, {'description':[b'foreign']}):
                backend.read.return_value = row
                with self.assertRaises(ValueError): worker.mail_resources()
            backend.read.return_value = {'description':[b'ANAS mail directory/v1']}
            self.assertIsInstance(worker.mail_resources(), PrimaryCoordinator)

    def test_native_person_new_resource_and_noop(self):
        db = Backend()
        self.assertTrue(db.run())
        box = db.rows[locator(BASE, owner=db.owner)]
        self.assertNotIn(one(box, 'anasMailboxID'), (db.owner, db.guid))
        before = copy.deepcopy((db.rows, db.people))
        writes = db.writes
        self.assertFalse(db.run())
        self.assertEqual((db.rows, db.people), before)
        self.assertEqual(db.writes, writes)

    def test_suffix_change_and_return_preserve_identity_and_all_proxy_values(self):
        db = Backend(); db.run()
        db.people[DN]['proxyAddresses'] += [b'SMTP:custom@example.test', b'X500:/history']
        box_id = one(db.rows[locator(BASE, owner=db.owner)], 'anasMailboxID')
        self.assertTrue(db.run('other.test'))
        old = db.rows[locator(BASE, email='alice@example.test')]
        self.assertEqual(one(old, 'anasMailAddressKind'), 'alias')
        self.assertEqual(one(old, 'anasMailState'), 'active')
        self.assertTrue(db.run('example.test'))
        self.assertEqual(one(db.rows[locator(BASE, owner=db.owner)], 'anasMailboxID'), box_id)
        self.assertEqual(set(db.people[DN]['proxyAddresses']), {b'smtp:alice@example.test', b'smtp:alice@other.test', b'SMTP:custom@example.test', b'X500:/history'})

    def test_every_mutation_interruption_and_lost_reply_resumes_without_new_box(self):
        for suffix in (False, True):
            baseline = Backend(); baseline.run()
            if not suffix:
                baseline = Backend()
            domain = 'other.test' if suffix else 'example.test'
            probe = copy.deepcopy((baseline.rows, baseline.people))
            before = baseline.writes; baseline.run(domain)
            count = baseline.writes - before
            for lost in (False, True):
                for step in range(1, count + 1):
                    with self.subTest(suffix=suffix, lost=lost, step=step):
                        db = Backend(); db.rows, db.people = copy.deepcopy(probe)
                        db.owner = one(db.people[DN], 'anasIdentityAnchor')
                        db.guid = str(uuid.UUID(bytes_le=db.people[DN]['objectGUID'][0]))
                        db.fail_at, db.lost = step, lost
                        with self.assertRaises(OSError): db.run(domain)
                        persisted = db.read(locator(BASE, owner=db.owner))
                        prior_id = one(persisted, 'anasMailboxID') if persisted else None
                        db.fail_at = None; db.run(domain)
                        if prior_id:
                            self.assertEqual(one(db.read(locator(BASE, owner=db.owner)), 'anasMailboxID'), prior_id)
                        self.assertFalse(db.run(domain))
                        self.assertEqual(one(db.read(locator(BASE, owner=db.owner)), 'anasMailState'), 'active')

    def test_foreign_primary_additional_alias_pending_and_retained_claims_not_adopted(self):
        for kind, state, owner in [('primary','active','foreign'), ('alias','active','foreign'), ('additional','active','foreign'), ('alias','retained','same'), ('alias','pending','same')]:
            with self.subTest(kind=kind, state=state, owner=owner):
                db = Backend(); db.run()
                box_id = one(db.read(locator(BASE, owner=db.owner)), 'anasMailboxID')
                email = 'alice@other.test'; dn = locator(BASE, email=email)
                row = encoded(objectClass='anasMailAddressClaim', anasMailOwnerAnchor=db.owner if owner=='same' else str(uuid.uuid4()), anasMailboxID=box_id,
                              anasMailAddress=email, anasMailAddressKind=kind, anasMailState=state,
                              anasMailConfigVersion=str(uuid.uuid4()), anasMailOperationID=str(uuid.uuid4()), anasMailOperationState='complete' if state=='active' else 'reserved')
                db.rows[dn] = row
                with self.assertRaises(ValueError): db.run('other.test')
                self.assertEqual(db.rows[dn], row)
                self.assertEqual(one(db.people[DN], 'mail'), 'alice@example.test')

    def test_missing_completed_claim_replaced_person_duplicate_anchor_and_rename_fail_closed(self):
        for variant in ('claim_missing', 'new_guid', 'duplicate', 'rename'):
            with self.subTest(variant=variant):
                db = Backend(); db.run()
                if variant == 'claim_missing': del db.rows[locator(BASE, email='alice@example.test')]
                if variant == 'new_guid': db.people[DN]['objectGUID'] = [uuid.uuid4().bytes_le]
                if variant == 'duplicate': db.people['CN=other,OU=People,'+BASE] = dict(db.people[DN], objectGUID=[uuid.uuid4().bytes_le])
                if variant == 'rename': db.people[DN]['sAMAccountName'] = [b'alice2']
                before = copy.deepcopy((db.rows, db.people))
                with self.assertRaises(ValueError): db.run('other.test')
                self.assertEqual((db.rows, db.people), before)

    def test_policy_change_during_pending_does_not_retarget(self):
        db = Backend(); db.run(); db.fail_at = db.writes + 2
        with self.assertRaises(OSError): db.run('other.test')
        before = copy.deepcopy((db.rows, db.people)); db.fail_at = None
        with self.assertRaisesRegex(ValueError, 'policy changed'): db.run('third.test')
        self.assertEqual((db.rows, db.people), before)

    def test_concurrent_proxy_add_is_preserved_and_unknown_readback_recovers(self):
        db = Backend(); db.run()
        update = db.update_person
        def concurrent(dn, before, email, proxies):
            db.people[dn]['proxyAddresses'].append(b'smtp:side@example.test')
            update(dn, before, email, proxies)
        db.update_person = concurrent
        with self.assertRaisesRegex(RuntimeError, 'result unknown'): db.run('other.test')
        self.assertIn(b'smtp:side@example.test', db.people[DN]['proxyAddresses'])
        self.assertEqual(one(db.read(locator(BASE, owner=db.owner)), 'anasMailState'), 'pending')
        db.update_person = update
        self.assertTrue(db.run('other.test'))
        self.assertIn(b'smtp:side@example.test', db.people[DN]['proxyAddresses'])

    def test_legacy_conflict_including_whitespace_rejected_before_write(self):
        db = Backend(); db.run()
        db.people['CN=foreign,'+BASE] = {'objectGUID':[uuid.uuid4().bytes_le], 'proxyAddresses':[b' SMTP:ALICE@OTHER.TEST ']}
        before = copy.deepcopy((db.rows, db.people))
        with self.assertRaisesRegex(ValueError, 'namespace conflict'): db.run('other.test')
        self.assertEqual((db.rows, db.people), before)


if __name__ == '__main__': unittest.main()
