"""Offline rejection controls for the disposable baked-image smoke test."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('runner_image', Path(__file__).with_name('server-incus-runner-image-e2e.py'))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class RunnerImageSafety(unittest.TestCase):
    def runner_events(self):
        children = sorted(lab.REQUIRED_RUNNER_TESTS - {lab.RUNNER_TEST})
        pairs = [('start', None), ('run', lab.RUNNER_TEST)]
        pairs.extend((action, name) for name in children for action in ('run', 'pass'))
        pairs.extend([('pass', lab.RUNNER_TEST), ('pass', None)])
        return [dict({'Action': action, 'Package': lab.RUNNER_PACKAGE},
                     **({'Test': name} if name else {})) for action, name in pairs]

    def test_native_image_gate_requires_one_actual_run_and_pass_per_test(self):
        events = self.runner_events()
        self.assertTrue(lab.runner_events_passed(events, 0))
        for changed in (
            [event for event in events if event['Action'] != 'run'],
            [*events, events[-2]],  # Duplicate parent pass.
            [*events, events[3]],  # Duplicate subtest pass.
            [*events, events[-1]],  # Duplicate package terminal event.
            [*events, {'Action': 'pass', 'Package': lab.RUNNER_PACKAGE, 'Test': 'mock-image'}],
            [{**event, 'Package': 'unrelated/package'} for event in events],
        ):
            with self.subTest(changed=changed[-1]):
                self.assertFalse(lab.runner_events_passed(changed, 0))

    def test_native_image_gate_never_substitutes_partial_or_failed_execution(self):
        events = self.runner_events()
        for code in (1, -9, '0', False, None):
            with self.subTest(code=code):
                self.assertFalse(lab.runner_events_passed(events, code))
        for changed in (events[:-1], events[:-2], [],
                        events+[{'Action': 'skip', 'Package': lab.RUNNER_PACKAGE}],
                        events+[{'Action': 'fail', 'Package': lab.RUNNER_PACKAGE}]):
            self.assertFalse(lab.runner_events_passed(changed, 0))

    def test_native_image_gate_rejects_malformed_and_impossible_event_order(self):
        events = self.runner_events()
        swapped = list(events)
        swapped[2], swapped[3] = swapped[3], swapped[2]
        for changed in (None, 'not-events', events+[None], events+[[]], events+[{}],
                        events+[{'Action': 'pass', 'Package': lab.RUNNER_PACKAGE, 'Test': []}],
                        events+[{'Action': 'unexpected', 'Package': lab.RUNNER_PACKAGE}],
                        [events[-1], *events[:-1]], swapped,
                        [events[0], events[-2], *events[1:-2], events[-1]]):
            with self.subTest(changed=type(changed).__name__):
                self.assertFalse(lab.runner_events_passed(changed, 0))
        for invalid in (None, [], {},
                        {'Action': 'pass', 'Package': lab.RUNNER_PACKAGE, 'Test': []},
                        {'Action': 'output', 'Package': lab.RUNNER_PACKAGE, 'Test': None},
                        {'Action': 'unexpected', 'Package': lab.RUNNER_PACKAGE}):
            self.assertFalse(lab.runner_events_passed([*events[:2], invalid, *events[2:]], 0))

    def test_staging_capacity_requires_all_parts_and_headroom(self):
        release = {'artifact': {'parts': [{'size': 712}, {'size': 244748288}]}}
        required = 712 + 244748288 + lab.STAGING_HEADROOM
        for available in (0, required - 1):
            with self.subTest(available=available), patch.object(lab.os, 'statvfs', return_value=SimpleNamespace(f_bavail=available, f_frsize=1)), patch.object(lab.subprocess, 'run') as run:
                with self.assertRaises(RuntimeError):
                    lab.require_staging_space(release)
                run.assert_not_called()
        with patch.object(lab.os, 'statvfs', return_value=SimpleNamespace(f_bavail=required, f_frsize=1)):
            lab.require_staging_space(release)

    def test_staging_capacity_observation_errors_are_private(self):
        release = {'artifact': {'parts': [{'size': 712}, {'size': 244748288}]}}
        with patch.object(lab.os, 'statvfs', side_effect=OSError('private-marker')):
            with self.assertRaises(RuntimeError) as error:
                lab.require_staging_space(release)
            self.assertNotIn('private-marker', str(error.exception))

    def test_invalid_sizes_cannot_bypass_capacity_check(self):
        for sizes in ((True, 10), (0, 10), (-1, 10), ('12', 10), (10,)):
            with self.subTest(sizes=sizes), patch.object(lab.os, 'statvfs') as observe:
                with self.assertRaises(RuntimeError):
                    lab.require_staging_space({'artifact': {'parts': [{'size': s} for s in sizes]}})
                observe.assert_not_called()

    def test_capacity_refusal_precedes_all_main_filesystem_and_daemon_effects(self):
        args = SimpleNamespace(vm_id='anas-runner-bake-abc123', exported_image='/export', report_root='/new-report',
                               provider='/provider', tests='/tests', test2json='/test2json', expected_fingerprint='a'*64)
        with patch.object(lab, 'require_vm'), patch.object(lab.Path, 'exists', return_value=False), \
                patch.object(lab.Path, 'is_symlink', return_value=False), patch.object(lab.Path, 'is_file', return_value=True), \
                patch.object(lab, 'verify_export', return_value={}), \
                patch.object(lab, 'require_staging_space', side_effect=RuntimeError('insufficient staging capacity')), \
                patch.object(lab.Path, 'mkdir') as mkdir, patch.object(lab.Path, 'write_text') as write, \
                patch.object(lab.subprocess, 'run') as run:
            with self.assertRaises(RuntimeError):
                lab.main(args)
            mkdir.assert_not_called()
            write.assert_not_called()
            run.assert_not_called()

    def test_non_root_cannot_read_host_or_execute(self):
        with patch.object(lab.os, 'geteuid', return_value=1000), patch.object(lab.Path, 'read_text') as read, patch.object(lab.subprocess, 'run') as run:
            with self.assertRaises(RuntimeError):
                lab.require_vm('anas-runner-bake-abc123')
            read.assert_not_called()
            run.assert_not_called()

    def test_vm_identity_vendor_and_docker_are_mandatory(self):
        for identity, vendor, docker in [('wrong', 'QEMU', False), ('anas-runner-bake-abc123', 'Dell', False), ('anas-runner-bake-abc123', 'QEMU', True)]:
            with self.subTest(identity=identity, vendor=vendor, docker=docker), patch.object(lab.os, 'geteuid', return_value=0), patch.object(lab.Path, 'read_text', side_effect=[identity, vendor]), patch.object(lab.Path, 'exists', return_value=docker), patch.object(lab.subprocess, 'run') as run:
                with self.assertRaises(RuntimeError):
                    lab.require_vm('anas-runner-bake-abc123')
                run.assert_not_called()

    def make_export(self, root):
        parts = []
        combined = hashlib.sha256()
        for name, role, body in [('incus.tar.xz', 'metadata', b'metadata-fixture'), ('rootfs.squashfs', 'rootfs', b'rootfs-fixture')]:
            (root/name).write_bytes(body)
            combined.update(body)
            parts.append({'role': role, 'sha256': hashlib.sha256(body).hexdigest(), 'size': len(body)})
        pin = combined.hexdigest()
        release = {'entry': {'name': 'forgejo-runner', 'architecture': 'amd64', 'interface': 'incus_container', 'fingerprint': pin},
                   'artifact': {'format': 'split', 'target': {'architecture': 'amd64', 'interface': 'incus_container'}, 'fingerprint': pin, 'parts': parts}}
        (root/'artifact.json').write_text(json.dumps(release))
        return release, pin

    def test_export_bytes_and_independent_fingerprint_are_checked(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            release, pin = self.make_export(root)
            self.assertEqual(lab.verify_export(root, pin), release)
            with self.assertRaises(RuntimeError):
                lab.verify_export(root, 'f'*64)
            (root/'rootfs.squashfs').write_bytes(b'corrupt-rootfs!')
            with self.assertRaises(RuntimeError):
                lab.verify_export(root, pin)

    def test_export_symlink_is_not_an_image_input(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _, pin = self.make_export(root)
            original = root/'rootfs.squashfs'
            original.rename(root/'displaced')
            original.symlink_to('displaced')
            with self.assertRaises(RuntimeError):
                lab.verify_export(root, pin)


if __name__ == '__main__':
    unittest.main()
