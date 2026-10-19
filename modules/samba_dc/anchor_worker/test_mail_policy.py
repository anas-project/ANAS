# TEST_CASES: MAIL-T-002
"""Primary address policy: collision preflight and truthful write outcomes."""
import types
import unittest
import uuid
from unittest.mock import patch

from anchor_worker import Directory, Settings
from mail_policy import default_address, desired_mail, mail_domain, plan_mail
from pathlib import Path


def person(name='alice', address=None, **overrides):
    attrs = {'objectGUID': [uuid.uuid4().bytes_le], 'objectClass': [b'top', b'user'],
             'sAMAccountName': [name.encode('ascii')], 'userAccountControl': [b'512'],
             'objectSid': [b'\x01\x02\x00\x00\x00\x00\x00\x05' + (21).to_bytes(4, 'little') + (1100).to_bytes(4, 'little')]}
    if address:
        attrs['mail'] = [address.encode('ascii')]
    attrs.update(overrides)
    return ('CN=' + name + ',OU=People,DC=example,DC=test', attrs)


class MailPolicyTest(unittest.TestCase):
    def test_default_domain_and_address_are_canonical(self):
        self.assertEqual(mail_domain('HLong.Wang.'), 'hlong.wang')
        self.assertEqual(default_address('Admin', 'finance.hlong.wang'), b'admin@finance.hlong.wang')
        self.assertEqual(default_address('_person', 'hlong.wang'), b'_person@hlong.wang')

    def test_invalid_names_and_domains_fail_without_sanitizing(self):
        for name in ['first last', 'a@b', 'a..b', 'a.', 'a/b', '张三', 'Ken', 'İan', 'a' * 65]:
            with self.subTest(name=name), self.assertRaises(ValueError):
                default_address(name, 'hlong.wang')
        for domain in ['', '127.0.0.1', 'a..b', 'https://hlong.wang', '-a.test']:
            with self.subTest(domain=domain), self.assertRaises(ValueError):
                mail_domain(domain)

    def test_migration_includes_existing_and_missing_addresses(self):
        people = [person('alice', 'alice@finance.hlong.wang'), person('bob'), person('carol', 'manual@example.test')]
        before = [(dn, dict(attrs)) for dn, attrs in people]
        plan = plan_mail(people, people, 'hlong.wang')
        self.assertEqual([p[2] for p in plan], [b'alice@hlong.wang', b'bob@hlong.wang', b'carol@hlong.wang'])
        self.assertEqual(people, before)
        current = person('current', 'current@hlong.wang')
        self.assertEqual(plan_mail([current], [current], 'hlong.wang'), [])

    def test_namespace_conflicts_are_rejected_before_any_changes(self):
        alice, bob = person(), person('bob')
        for attr, value in [('mail', b'alice@hlong.wang'), ('proxyAddresses', b'SMTP:ALICE@HLONG.WANG')]:
            outside = person('outside', **{attr: [value]})
            with self.subTest(attribute=attr), self.assertRaises(ValueError):
                plan_mail([bob, alice], [alice, bob, outside], 'hlong.wang')
        duplicate = person('ALICE')
        with self.assertRaises(ValueError):
            plan_mail([alice, duplicate], [], 'hlong.wang')

    def test_service_computer_and_builtin_accounts_are_not_targets(self):
        for override in [{'objectClass': [b'user', b'computer']}, {'userAccountControl': [b'4096']}, {'sAMAccountName': [b'krbtgt_1']},
                         {'objectSid': [b'\x01' * 8 + (500).to_bytes(4, 'little')]}]:
            self.assertIsNone(desired_mail(person(**override)[1], 'hlong.wang'))

    def test_missing_identity_or_account_flags_stops_the_plan(self):
        for override in [{'objectGUID': []}, {'objectGUID': [b'short']}, {'userAccountControl': []}, {'sAMAccountName': []}]:
            with self.subTest(override=override), self.assertRaises(ValueError):
                plan_mail([person(**override)], [], 'hlong.wang')


class MailWriteTest(unittest.TestCase):
    def directory(self, rows, failure=None):
        directory = object.__new__(Directory)
        directory.settings = Settings('ldaps://dc:636', 'CN=svc', 'fixture', (), (), 'mS-DS-ConsistencyGuid', 'anasIdentityAnchor', Path('/tmp/audit'), Path('/tmp/health'), '/certs/ca', 300, 500, mail_domain='hlong.wang', mail_user_base='OU=People,DC=example,DC=test', mail_search_base='DC=example,DC=test')
        directory.mail_in_scope = lambda dn: dn.endswith(',OU=People,DC=example,DC=test')
        directory.read_target = lambda _dn: rows.pop(0)
        self.writes = []
        def modify(dn, changes):
            self.writes.append((dn, changes))
            if failure:
                raise failure
        directory.connect = lambda: types.SimpleNamespace(modify_s=modify)
        return directory

    def test_timeout_is_never_replayed_or_reported_as_success(self):
        row = person()
        directory = self.directory([row], TimeoutError('unknown result'))
        with patch('anchor_worker.ldap', types.SimpleNamespace(MOD_DELETE=1, MOD_ADD=0)), self.assertRaises(TimeoutError):
            directory.save_mail(*row, b'alice@hlong.wang')
        self.assertEqual(len(self.writes), 1)
        self.assertTrue(self.writes[0][0].startswith('<GUID='))
        self.assertEqual([item[1] for item in self.writes[0][1]], ['mail'])

    def test_moved_or_renamed_target_is_not_written(self):
        row = person()
        for changed in [('CN=alice,OU=Other,DC=example,DC=test', row[1]), (row[0], {**row[1], 'sAMAccountName': [b'newname']})]:
            directory = self.directory([changed])
            with self.assertRaises(RuntimeError):
                directory.save_mail(*row, b'alice@hlong.wang')
            self.assertEqual(self.writes, [])

    def test_success_requires_fresh_readback(self):
        row = person(address='alice@finance.hlong.wang')
        saved = (row[0], {**row[1], 'mail': [b'alice@hlong.wang']})
        for verification, expected in [(saved, True), (row, False)]:
            directory = self.directory([row, verification])
            with patch('anchor_worker.ldap', types.SimpleNamespace(MOD_DELETE=1, MOD_ADD=0)):
                if expected:
                    self.assertTrue(directory.save_mail(*row, b'alice@hlong.wang'))
                else:
                    with self.assertRaises(RuntimeError):
                        directory.save_mail(*row, b'alice@hlong.wang')
            self.assertEqual(len(self.writes), 1)
            self.assertEqual([item[1] for item in self.writes[0][1]], ['mail', 'mail'])


if __name__ == "__main__":
    unittest.main()
