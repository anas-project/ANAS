"""Offline safety gates for the explicit disposable-VM lifecycle harness."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('lifecycle',Path(__file__).with_name('server-incus-lifecycle-e2e.py'))
lab=importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class LifecycleHarnessSafety(unittest.TestCase):
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
