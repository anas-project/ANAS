"""Offline safety gates for the explicit disposable-VM fence harness."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('fence', Path(__file__).with_name('server-incus-fence-e2e.py'))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class FenceHarnessSafety(unittest.TestCase):
    def test_every_generation_requires_its_own_daemon_checks(self):
        self.assertFalse({'storage_pool_escape_rejected', 'vm_nesting_rejected'} & lab.required_checks('6.0'))
        self.assertIn('storage_pool_escape_rejected', lab.required_checks('7.0'))
        self.assertNotIn('vm_nesting_rejected', lab.required_checks('7.0'))
        self.assertTrue({'image_servers_block_local_create', 'vm_nesting_rejected'} <= lab.required_checks('7.5'))
        self.assertTrue(set(lab.BASE_CHECKS) <= lab.required_checks('6.0'))

    def test_missing_or_failed_check_fails_the_gate(self):
        for generation in lab.GENERATIONS:
            results = [{'check': name, 'passed': True} for name in lab.required_checks(generation)]
            self.assertTrue(lab.checks_passed(results, generation))
            for name in lab.required_checks(generation):
                with self.subTest(generation=generation, name=name):
                    missing = [item for item in results if item['check'] != name]
                    self.assertFalse(lab.checks_passed(missing, generation))
                    self.assertFalse(lab.checks_passed(missing + [{'check': name, 'passed': False}], generation))
            self.assertFalse(lab.checks_passed(results + [{'check': 'extra', 'passed': False}], generation))

    def test_only_a_synchronous_refusal_with_the_daemon_reason_counts(self):
        reason = 'Reached maximum number of instances of type "container"'
        self.assertTrue(lab.classify(403, {'type': 'error', 'error': reason + ' in project "p"'}, reason)['refused'])
        self.assertFalse(lab.classify(403, {'type': 'error', 'error': 'Certificate is restricted'}, reason)['refused'])
        self.assertFalse(lab.classify(202, {'type': 'async', 'operation': '/1.0/operations/x'}, reason)['refused'])
        self.assertTrue(lab.classify(202, {'type': 'async', 'operation': '/1.0/operations/x'}, '')['admitted'])

    def test_exact_identity_and_no_docker_are_required(self):
        for identity, vendor, docker in [('anas-incus-fence-other1', 'QEMU', False), ('anas-incus-fence-abc123', 'Dell', False),
                                         ('anas-incus-fence-abc123', 'QEMU', True)]:
            with self.subTest(identity=identity, vendor=vendor, docker=docker), patch.object(lab.os, 'geteuid', return_value=0), \
                    patch.object(lab.Path, 'read_text', side_effect=[identity, vendor]), \
                    patch.object(lab.Path, 'exists', return_value=docker), patch.object(lab.subprocess, 'run') as run:
                with self.assertRaises(RuntimeError):
                    lab.require_vm('anas-incus-fence-abc123')
                run.assert_not_called()

    def test_unprivileged_owner_rejected_before_read_or_exec(self):
        with patch.object(lab.os, 'geteuid', return_value=1000), patch.object(lab.Path, 'read_text') as read, \
                patch.object(lab.subprocess, 'run') as run:
            with self.assertRaises(RuntimeError):
                lab.require_vm('anas-incus-fence-abc123')
            read.assert_not_called(); run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
