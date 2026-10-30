# TEST_CASES: MAILMOD-T-010
"""Ensure visible To never changes the controlled SMTP envelope."""
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


SCRIPT = Path(__file__).with_name('mail-cloudflare-smtp-send.py')


class FakeSMTP:
    submitted = []

    def __init__(self, *args, **kwargs):
        pass

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False

    def ehlo(self):
        return (250, b'ok')

    def login(self, *args):
        return (235, b'ok')

    def data(self, message):
        return (250, b'ok')

    def sendmail(self, sender, recipients, message):
        self.submitted.append((sender, recipients, message_from_bytes(message)))
        self.data(message)
        return {}


class VisibleRecipientTest(unittest.TestCase):
    def test_controlled_header_does_not_become_envelope_recipient(self):
        self.check_domain('mail-poc.bengbu.cc.cd')
        self.check_domain('hlong.wang')

    def check_domain(self, domain):
        with tempfile.TemporaryDirectory() as directory:
            paths = [Path(directory) / name for name in ('key.json', 'input.json')]
            recipients = [f'smtp-abcdef123456-{kind}@{domain}' for kind in ('main', 'alias', 'extra')]
            key = {'domain': 'mail-poc.hlong.wang', 'provider': 'eusend', 'smtp_host': 'smtp.eusend.dev', 'smtp_port': 465, 'smtp_username': 'eusend', 'api_key': 'synthetic'}
            cfg = {'Routes': [{'address': address} for address in recipients], 'VisibleTo': 'external-test@126.com', 'Subject': 'fixture', 'Source': {'ID': '11111111-1111-4111-8111-111111111111'}}
            for path, value in zip(paths, (key, cfg)):
                path.write_text(json.dumps(value))
                os.chmod(path, 0o600)
            FakeSMTP.submitted = []
            output = io.StringIO()
            with patch('smtplib.SMTP_SSL', FakeSMTP), patch.object(sys, 'argv', [str(SCRIPT), *map(str, paths)]), contextlib.redirect_stdout(output):
                runpy.run_path(str(SCRIPT), run_name='__main__')
            self.assertEqual(len(FakeSMTP.submitted), 1)
            sender, envelope, message = FakeSMTP.submitted[0]
            self.assertEqual(sender, 'probe@mail-poc.hlong.wang')
            self.assertEqual(envelope, recipients)
            self.assertEqual(message['To'], cfg['VisibleTo'])
            self.assertNotIn(cfg['VisibleTo'], envelope)
            self.assertTrue(json.loads(output.getvalue())['visible_to_is_foreign'])


if __name__ == '__main__':
    unittest.main()
