# TEST_CASES: MAILMOD-T-010
import contextlib
import io
import json
import os
from pathlib import Path
import runpy
import sys
import tempfile
import unittest
from email import message_from_bytes
from unittest.mock import patch

SCRIPT = Path(__file__).with_name('mail-cloudflare-direct-smtp-send.py')

class FakeSMTP:
    events = []
    reject = False
    def __init__(self, *args, **kwargs): pass
    def __enter__(self): return self
    def __exit__(self, *args): return False
    def ehlo(self): return (250, b'ok')
    def has_extn(self, name): return name == 'starttls'
    def starttls(self, **kwargs): return (220, b'ok')
    def mail(self, sender): self.events.append(('mail', sender)); return (250, b'ok')
    def rcpt(self, recipient):
        self.events.append(('rcpt', recipient))
        return (550 if self.reject and '-alias@' in recipient else 250, b'ok')
    def rset(self): self.events.append(('rset',)); return (250, b'ok')
    def data(self, raw): self.events.append(('data', message_from_bytes(raw))); return (250, b'ok')

class DirectSenderTest(unittest.TestCase):
    def run_fixture(self, reject=False):
        with tempfile.TemporaryDirectory() as directory:
            binding = Path(directory) / 'input.json'
            addresses = [f'smtp-123456abcdef-{kind}@mail-poc.bengbu.cc.cd' for kind in ('main', 'alias', 'extra')]
            source = '11111111-1111-4111-8111-111111111111'
            binding.write_text(json.dumps({'Routes': [{'address': a} for a in addresses], 'VisibleTo': 'external-test@126.com', 'Subject': 'ANAS cloud SMTP '+source, 'Source': {'ID': source}}))
            os.chmod(binding, 0o600)
            FakeSMTP.events, FakeSMTP.reject = [], reject
            with patch('smtplib.SMTP', FakeSMTP), patch.object(sys, 'argv', [str(SCRIPT), str(binding), 'route1.mx.cloudflare.net', '17142']), contextlib.redirect_stdout(io.StringIO()):
                if reject:
                    with self.assertRaisesRegex(RuntimeError, 'recipient rejected before DATA'):
                        runpy.run_path(str(SCRIPT), run_name='__main__')
                else:
                    runpy.run_path(str(SCRIPT), run_name='__main__')
            return addresses, FakeSMTP.events

    def test_fixed_envelope_and_external_visible_header(self):
        addresses, events = self.run_fixture()
        self.assertEqual([e[1] for e in events if e[0] == 'rcpt'], addresses)
        messages = [e[1] for e in events if e[0] == 'data']
        self.assertEqual(len(messages), 1)
        self.assertEqual(messages[0]['To'], 'external-test@126.com')
        self.assertNotIn(messages[0]['To'], addresses)

    def test_partial_recipient_rejection_sends_no_data(self):
        _, events = self.run_fixture(reject=True)
        self.assertFalse(any(e[0] == 'data' for e in events))
        self.assertEqual(events[-1], ('rset',))

if __name__ == '__main__': unittest.main()
