import importlib.util
import os
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('host_native', Path(__file__).with_name('server-incus-host-provision-e2e.py'))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class HostProvisionNativeSafety(unittest.TestCase):
    def valid_events(self):
        return [dict(Action=action, Test=name) for name in sorted(lab.REQUIRED) for action in ('run', 'pass')]

    def test_wrong_machine_rejected_before_any_subprocess(self):
        with patch.object(lab.os, 'geteuid', return_value=1000), patch.object(lab.Path, 'read_text') as read, patch.object(lab.subprocess, 'run') as run:
            with self.assertRaises(RuntimeError):
                lab.require_vm('anas-incus-host-abc123')
            read.assert_not_called()
            run.assert_not_called()
        for actual, vendor in [('production-vm', 'QEMU'), ('anas-incus-host-abc123', 'Dell')]:
            with patch.object(lab.os, 'geteuid', return_value=0), patch.object(lab.Path, 'read_text', side_effect=[actual, vendor]), patch.object(lab.subprocess, 'run') as run:
                with self.assertRaises(RuntimeError):
                    lab.require_vm('anas-incus-host-abc123')
                run.assert_not_called()

    def test_other_lab_identity_is_not_authorization(self):
        with patch.object(lab.os, 'geteuid', return_value=0), patch.object(lab.Path, 'read_text') as read:
            for identity in ('anas-incus-build-abc123', 'anas-incus-lifecycle-abc123', 'production-vm', '', 'anas-incus-host-abc123\n'):
                with self.subTest(identity=identity), self.assertRaises(RuntimeError):
                    lab.require_vm(identity)
            read.assert_not_called()

    def test_writable_report_ancestor_rejected_before_marker_or_docker(self):
        args = SimpleNamespace(vm_id='anas-incus-host-abc123', tests='/opt/fixture/tests',
                               test2json='/opt/fixture/test2json', report_root='/var/log/fixture/run')
        info = SimpleNamespace(st_mode=lab.stat.S_IFDIR | 0o775, st_uid=0)
        with patch.object(lab, 'require_vm'), patch.object(lab, 'protected_input', return_value='a'*64), \
                patch.object(lab.Path, 'resolve', autospec=True, side_effect=lambda path: path), \
                patch.object(lab.Path, 'exists', return_value=False), \
                patch.object(lab.Path, 'lstat', return_value=info), \
                patch.object(lab.Path, 'mkdir') as mkdir, patch.object(lab.subprocess, 'run') as run:
            with self.assertRaisesRegex(RuntimeError, 'protected root-owned ancestors'):
                lab.main(args)
            mkdir.assert_not_called()
            run.assert_not_called()

    def test_all_required_events_and_zero_exit_are_mandatory(self):
        events = self.valid_events()
        self.assertEqual(len(lab.REQUIRED), 10)
        self.assertIn(lab.PARENT+'/uninstall_preflight_preserves_retained_storage', lab.REQUIRED)
        self.assertTrue(lab.native_events_passed(events, 0))
        self.assertFalse(lab.native_events_passed(events, 1))
        self.assertFalse(lab.native_events_passed([], 0))
        for index in range(len(events)):
            self.assertFalse(lab.native_events_passed(events[:index]+events[index+1:], 0))

    def test_skips_failures_duplicates_and_unrelated_tests_never_pass(self):
        events = self.valid_events()
        for extra in (dict(Action='skip', Test=lab.PARENT), dict(Action='fail', Test=lab.PARENT),
                      dict(Action='fail'), dict(Action='pass', Test=lab.PARENT),
                      dict(Action='run', Test='OtherTest'), dict(Action='pass', Test='OtherTest'), []):
            self.assertFalse(lab.native_events_passed(events+[extra], 0))
        self.assertTrue(lab.native_events_passed(events+[dict(Action='output', Output='redacted diagnostic'), dict(Action='pass')], 0))

    def test_log_budget_does_not_limit_package_or_storage_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = root/'package-index'
            code = 'import os,sys; f=open(sys.argv[1],"wb"); f.truncate(40<<20); f.seek((40<<20)-1); f.write(b"x"); f.close(); print("complete")'
            result = lab.run_native_process([sys.executable, '-c', code, str(data)], dict(os.environ),
                                            root/'stdout', root/'stderr', 10, max_log_bytes=1024)
            self.assertEqual(result, (0, False, False))
            self.assertEqual(data.stat().st_size, 40 << 20)
            self.assertEqual((root/'stdout').read_text(), 'complete\n')

    def test_output_overflow_is_bounded_and_never_success_evidence(self):
        for stream in (1, 2):
            with self.subTest(stream=stream), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                code = 'import os,sys; os.write(int(sys.argv[1]), b"x"*4096)'
                result = lab.run_native_process([sys.executable, '-c', code, str(stream)], dict(os.environ),
                                                root/'stdout', root/'stderr', 10, max_log_bytes=1024)
                self.assertTrue(result[2])
                self.assertFalse(result[1])
                self.assertLessEqual((root/'stdout').stat().st_size, 1024)
                self.assertLessEqual((root/'stderr').stat().st_size, 1024)

    def test_timeout_still_applies_after_both_output_streams_close(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            code = 'import os,time; os.close(1); os.close(2); time.sleep(10)'
            result = lab.run_native_process([sys.executable, '-c', code], dict(os.environ),
                                            root/'stdout', root/'stderr', 0.5, max_log_bytes=1024)
            self.assertNotEqual(result[0], 0)
            self.assertTrue(result[1])
            self.assertFalse(result[2])


if __name__ == '__main__':
    unittest.main()
