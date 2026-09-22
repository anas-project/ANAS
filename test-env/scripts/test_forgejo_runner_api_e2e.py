"""Offline admission tests; never start the actual Forgejo service."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('forgejo_api_lab',Path(__file__).with_name('server-forgejo-runner-api-e2e.py'))
lab=importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class ForgejoAPIAdmission(unittest.TestCase):
    def test_native_password_format_and_ambiguous_output(self):
        line="generated random password is '"+'a'*48+"'\n"
        self.assertEqual(lab.generated_password(line),'a'*48)
        for text in (line+line,"password: "+'a'*48,"generated random password is 'short'",''):
            with self.assertRaises(RuntimeError) as caught:lab.generated_password(text)
            self.assertNotIn('a'*48,str(caught.exception))

    def test_root_is_rejected_before_reading_host_files(self):
        with patch.object(lab.os,'geteuid',return_value=0),patch.object(lab.Path,'read_text') as read,patch.object(lab.subprocess,'Popen') as process:
            with self.assertRaises(RuntimeError):lab.require_vm('anas-runner-bake-abc123')
            read.assert_not_called();process.assert_not_called()

    def test_identity_vendor_and_no_docker_required(self):
        for identity,vendor,docker in [('wrong','QEMU',False),('anas-runner-bake-abc123','Other',False),('anas-runner-bake-abc123','QEMU',True)]:
            with self.subTest(identity=identity,vendor=vendor,docker=docker),patch.object(lab.os,'geteuid',return_value=1000),patch.object(lab.Path,'read_text',side_effect=[identity,vendor]),patch.object(lab.Path,'exists',return_value=docker),patch.object(lab.subprocess,'Popen') as process:
                with self.assertRaises(RuntimeError):lab.require_vm('anas-runner-bake-abc123')
                process.assert_not_called()


if __name__=='__main__':unittest.main()
