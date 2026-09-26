"""Offline guards; none of these tests starts Forgejo or accesses a VM."""
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('account_native', Path(__file__).with_name('server-forgejo-account-e2e.py'))
NATIVE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(NATIVE)


class AccountAcceptanceGuards(unittest.TestCase):
    def test_exact_isolated_vm_only(self):
        identity = 'anas-incus-host-abcdef'
        facts = {'uid': 0, 'vendor': 'QEMU', 'identity': identity, 'docker_absent': True}
        NATIVE.validate_vm(identity, facts)
        for key, value in [('uid', 1000), ('vendor', 'physical'), ('identity', identity+'x'), ('docker_absent', False)]:
            with self.subTest(key=key), self.assertRaises(NATIVE.GateFailure):
                NATIVE.validate_vm(identity, {**facts, key: value})
        for name in ('', 'anas-incus-host-ABCDEF', identity+'\n', '../'+identity):
            with self.assertRaises(NATIVE.GateFailure):
                NATIVE.validate_vm(name, {**facts, 'identity': name})

    def test_only_all_unique_native_gates_establish_success(self):
        events = [{'stage': name, 'status': 'passed'} for name in NATIVE.REQUIRED]
        self.assertTrue(NATIVE.complete(events))
        self.assertFalse(NATIVE.complete(events[:-1]))
        self.assertFalse(NATIVE.complete(events+events[:1]))
        self.assertFalse(NATIVE.complete(events[:-1]+events[:1]))
        for status in ('failed', 'skipped', 'pending'):
            self.assertFalse(NATIVE.complete([*events[:-1], {'stage': events[-1]['stage'], 'status': status}]))

    def test_unexpected_error_text_is_not_public(self):
        self.assertEqual(str(NATIVE.GateFailure('PRIVATE KEY secret')), 'unexpected_failure')

    def test_anonymous_readiness_does_not_send_empty_basic_auth(self):
        self.assertNotIn('Authorization', NATIVE.request_headers('', ''))
        header = NATIVE.request_headers('owner', 'secret')
        self.assertTrue(header['Authorization'].startswith('Basic '))
        self.assertNotIn('secret', repr(header))

    def test_exact_public_executable_mode_despite_private_umask(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            old = os.umask(0o077)
            try:
                NATIVE.write_new(root/'binary', b'fixture executable bytes', 0o755)
                NATIVE.write_new(root/'private', b'private fixture bytes')
            finally:
                os.umask(old)
            self.assertEqual((root/'binary').stat().st_mode & 0o777, 0o755)
            self.assertEqual((root/'private').stat().st_mode & 0o777, 0o600)


if __name__ == '__main__':
    unittest.main()
