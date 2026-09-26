"""Offline safety gates for the explicit disposable-VM lifecycle harness."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('lifecycle',Path(__file__).with_name('server-incus-lifecycle-e2e.py'))
lab=importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class LifecycleHarnessSafety(unittest.TestCase):
    def test_rotation_fixture_binds_private_credentials_and_provider_bytes(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(lab, 'ROOT', Path(directory)):
            root = Path(directory)
            provider = root/'source-provider'
            provider.write_bytes(b'opaque-precompiled-provider-fixture')
            environments = [{'INCUS_ENDPOINT': 'https://127.0.0.1:8443',
                             'ANAS_RESOURCE_SANDBOX': project, 'INCUS_ADMIN_KEY_B64': 'private-fixture'}
                            for project in lab.PROJECTS]
            lab.prepare_rotation_fixture(provider, environments)
            record = json.loads((root/'rotation.json').read_text())
            self.assertEqual(record['environments'], environments)
            self.assertEqual(record['provider_sha256'], lab.hashlib.sha256(provider.read_bytes()).hexdigest())
            self.assertEqual((root/'provider').read_bytes(), provider.read_bytes())
            self.assertEqual((root/'provider').stat().st_mode & 0o777, 0o700)
            self.assertEqual((root/'rotation.json').stat().st_mode & 0o777, 0o600)
            before = (root/'rotation.json').read_bytes()
            with self.assertRaises(FileExistsError):
                lab.prepare_rotation_fixture(provider, environments)
            self.assertEqual((root/'rotation.json').read_bytes(), before)

    def test_rotation_fixture_rejects_other_endpoint_or_project_before_copy(self):
        for field, value in [('INCUS_ENDPOINT', 'https://example.invalid:8443'),
                             ('ANAS_RESOURCE_SANDBOX', 'other-project')]:
            with self.subTest(field=field), tempfile.TemporaryDirectory() as directory, patch.object(lab, 'ROOT', Path(directory)):
                environments = [{'INCUS_ENDPOINT': 'https://127.0.0.1:8443', 'ANAS_RESOURCE_SANDBOX': project}
                                for project in lab.PROJECTS]
                environments[0][field] = value
                with self.assertRaises(RuntimeError):
                    lab.prepare_rotation_fixture(Path(directory)/'nonexistent-provider', environments)
                self.assertFalse((Path(directory)/'provider').exists())
                self.assertFalse((Path(directory)/'rotation.json').exists())

    def test_rotation_and_proxy_cannot_be_missing_or_skipped_in_native_gate(self):
        events = [{'Action': 'pass', 'Test': name} for name in lab.required_lifecycle_tests()]
        events.append({'Action': 'pass'})
        self.assertTrue(lab.lifecycle_results_passed(events, 0))
        self.assertFalse(lab.lifecycle_results_passed(events, 1))
        for name in lab.required_lifecycle_tests():
            with self.subTest(name=name):
                missing = [event for event in events if event.get('Test') != name]
                self.assertFalse(lab.lifecycle_results_passed(missing, 0))
                self.assertFalse(lab.lifecycle_results_passed(missing + [{'Action': 'skip', 'Test': name}], 0))
        self.assertFalse(lab.lifecycle_results_passed(events[:-1], 0))

    def test_unprivileged_owner_rejected_before_read_or_exec(self):
        with patch.object(lab.os,'geteuid',return_value=1000),patch.object(lab.Path,'read_text') as read,patch.object(lab.subprocess,'run') as run:
            with self.assertRaises(RuntimeError):lab.require_vm('anas-incus-lifecycle-abc123')
            read.assert_not_called();run.assert_not_called()

    def test_exact_identity_and_no_docker_are_required(self):
        for identity,vendor,docker in [('anas-incus-lifecycle-other1','QEMU',False),('anas-incus-lifecycle-abc123','Dell',False),('anas-incus-lifecycle-abc123','QEMU',True)]:
            with self.subTest(identity=identity,vendor=vendor,docker=docker),patch.object(lab.os,'geteuid',return_value=0),patch.object(lab.Path,'read_text',side_effect=[identity,vendor]),patch.object(lab.Path,'exists',return_value=docker),patch.object(lab.subprocess,'run') as run:
                with self.assertRaises(RuntimeError):lab.require_vm('anas-incus-lifecycle-abc123')
                run.assert_not_called()

    def test_fixture_digest_is_real_bytes_and_rootfs_is_not_product(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);binary=root/'helper';binary.write_bytes(b'opaque-test-not-an-ELF')
            target=root/'fixture.tar.xz';digest=lab.create_fixture_image(binary,target)
            self.assertEqual(digest,lab.hashlib.sha256(target.read_bytes()).hexdigest())
            with lab.tarfile.open(target) as archive:
                self.assertIn(b'not a product image',archive.extractfile('metadata.yaml').read())
                self.assertEqual(archive.getmember('rootfs/sbin/init').linkname,'/usr/local/bin/anas-fixture')


if __name__=='__main__':unittest.main()
