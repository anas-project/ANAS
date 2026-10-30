# TEST_CASES: MAILMOD-T-009
"""Only explicit SMTP refusals may be reported as a known negative result."""
import contextlib
import io
import json
import os
from pathlib import Path
import runpy
import smtplib
import sys
import types
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).with_name('send-public.py')
KEY = {'provider': 'eusend', 'domain': 'mail-poc.hlong.wang',
       'smtp_host': 'smtp.eusend.dev', 'smtp_port': 465,
       'smtp_username': 'eusend', 'api_key': 'private-test-key'}
INPUT = {'recipient': 'cf-vps-test-abcdef123456@mail-poc.bengbu.cc.cd', 'nonce': 'a' * 32}


class FakeSMTP:
    failure = None

    def __init__(self, *args, **kwargs): pass
    def __enter__(self): return self
    def __exit__(self, *args): return False
    def ehlo(self): pass
    def login(self, *args): pass
    def sendmail(self, *args):
        if self.failure: raise self.failure
        return {}


class SubmitTest(unittest.TestCase):
    def run_with(self, failure):
        FakeSMTP.failure = failure
        stat = types.SimpleNamespace(st_uid=os.getuid(), st_mode=0o600)
        with patch.object(Path, 'lstat', return_value=stat), \
             patch.object(Path, 'is_symlink', return_value=False), \
             patch.object(Path, 'is_file', return_value=True), \
             patch.object(Path, 'read_text', return_value=json.dumps(KEY)), \
             patch('smtplib.SMTP_SSL', FakeSMTP), \
             patch.object(sys, 'stdin', io.StringIO(json.dumps(INPUT))), \
             contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaises(SystemExit) as result:
                runpy.run_path(str(SCRIPT), run_name='__main__')
        return str(result.exception)

    def test_explicit_refusals_are_bounded_and_redacted(self):
        self.assertEqual(self.run_with(smtplib.SMTPDataError(450, b'private provider text')), 'smtp-data-rejected:450')
        self.assertEqual(self.run_with(smtplib.SMTPRecipientsRefused({'private@example.test': (550, b'private')})),
                         'smtp-all-recipients-rejected-before-data')

    def test_connection_loss_remains_unknown(self):
        self.assertEqual(self.run_with(ConnectionError('private detail')),
                         'synthetic submission unconfirmed; do not retry blindly')


if __name__ == '__main__':
    unittest.main()
