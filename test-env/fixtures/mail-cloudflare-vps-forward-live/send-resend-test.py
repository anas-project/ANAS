# TEST_CASES: MAILMOD-T-009
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
from email import message_from_bytes
from unittest.mock import patch

SCRIPT = Path(__file__).with_name('send-resend.py')
KEY = {'provider': 'resend', 'domain': 'mail-poc.hlong.wang', 'smtp_host': 'smtp.resend.com',
       'smtp_port': 587, 'smtp_username': 'resend', 'api_key': 'private-test-key'}
RECIPIENT = 'cf-vps-test-abcdef123456@mail-poc.bengbu.cc.cd'


class FakeSMTP:
    messages = []
    failure = None
    tls = False
    def __init__(self, host, port, **kwargs):
        if (host, port) != ('smtp.resend.com', 587): raise AssertionError('wrong SMTP server')
    def __enter__(self): return self
    def __exit__(self, *args): return False
    def ehlo(self): return (250, b'ok')
    def starttls(self, *, context):
        if not context.check_hostname: raise AssertionError('TLS hostname verification disabled')
        self.tls = True
    def login(self, username, password):
        if not self.tls or username != 'resend' or password != 'private-test-key': raise AssertionError('bad login')
    def sendmail(self, sender, recipients, raw):
        self.messages.append((sender, recipients, message_from_bytes(raw)))
        if self.failure: raise self.failure
        return {}


class ResendTest(unittest.TestCase):
    def run_fixture(self, failure=None, recipient=RECIPIENT):
        FakeSMTP.messages, FakeSMTP.failure, FakeSMTP.tls = [], failure, False
        stat = types.SimpleNamespace(st_uid=os.getuid(), st_mode=0o600)
        with patch.object(Path, 'lstat', return_value=stat), patch.object(Path, 'is_symlink', return_value=False), \
             patch.object(Path, 'is_file', return_value=True), patch.object(Path, 'read_text', return_value=json.dumps(KEY)), \
             patch('smtplib.SMTP', FakeSMTP), patch.object(sys, 'stdin', io.StringIO(json.dumps({'recipient': recipient, 'nonce': 'a'*32}))), \
             contextlib.redirect_stdout(io.StringIO()):
            if failure or recipient != RECIPIENT:
                with self.assertRaises(SystemExit) as result: runpy.run_path(str(SCRIPT), run_name='__main__')
                return str(result.exception), FakeSMTP.messages
            runpy.run_path(str(SCRIPT), run_name='__main__')
        return '', FakeSMTP.messages

    def test_external_visible_to_never_enters_envelope(self):
        _, messages = self.run_fixture()
        self.assertEqual(len(messages), 1)
        sender, recipients, message = messages[0]
        self.assertEqual(sender, 'probe@mail-poc.hlong.wang')
        self.assertEqual(recipients, [RECIPIENT])
        self.assertEqual(message['To'], 'whlsxl@126.com')

    def test_explicit_reject_and_unknown_are_not_confused(self):
        self.assertEqual(self.run_fixture(smtplib.SMTPDataError(550, b'private provider text'))[0], 'smtp-data-rejected:550')
        self.assertEqual(self.run_fixture(ConnectionError('private detail'))[0], 'synthetic submission unconfirmed; do not retry blindly')
        self.assertEqual(self.run_fixture(recipient='other@example.test')[1], [])


if __name__ == '__main__': unittest.main()
