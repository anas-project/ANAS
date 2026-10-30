# TEST_CASES: MAILMOD-T-009
import contextlib
import io
import json
from pathlib import Path
import runpy
import sys
import unittest
from email import message_from_bytes
from unittest.mock import patch

SCRIPT = Path(__file__).with_name('send-direct.py')
RECIPIENT = 'cf-vps-test-abcdef123456@mail-poc.bengbu.cc.cd'


class FakeSMTP:
    events = []
    reject = False
    failure = None
    reject_first = False
    enhanced = False
    def __init__(self, host, *args, **kwargs): self.host = host
    def __enter__(self): return self
    def __exit__(self, *args): return False
    def ehlo(self):
        if self.failure == 'before': raise ConnectionError('private detail')
        return (250, b'ok')
    def has_extn(self, name): return name == 'starttls'
    def starttls(self, **kwargs): return (220, b'ok')
    def mail(self, sender): self.events.append(('mail', sender)); return (250, b'ok')
    def rcpt(self, recipient):
        self.events.append(('rcpt', recipient))
        return (550 if self.reject or self.reject_first and self.host != 'route3.mx.cloudflare.net' else 250, b'5.1.1 private@example.test' if self.enhanced else b'ok')
    def rset(self): self.events.append(('rset',)); return (250, b'ok')
    def data(self, raw):
        self.events.append(('data', message_from_bytes(raw)))
        if self.failure == 'data': raise ConnectionError('private detail')
        if self.failure == 'rejected': return (450, b'private detail')
        return (250, b'ok')


class DirectTest(unittest.TestCase):
    def run_fixture(self, recipient=RECIPIENT, reject=False, failure=None, all_mx=False, reject_first=False, enhanced=False, probe=False):
        FakeSMTP.events, FakeSMTP.reject, FakeSMTP.failure, FakeSMTP.reject_first, FakeSMTP.enhanced = [], reject, failure, reject_first, enhanced
        args = [str(SCRIPT), 'all' if all_mx else 'route3.mx.cloudflare.net', '17142']
        if probe: args.append('probe')
        with patch('smtplib.SMTP', FakeSMTP), patch('time.sleep'), patch.object(sys, 'argv', args), \
             patch.object(sys, 'stdin', io.StringIO(json.dumps({'recipient': recipient, 'nonce': 'a' * 32}))), \
             contextlib.redirect_stdout(io.StringIO()):
            if failure:
                expected = {'before':'smtp-pre-data-failed', 'data':'smtp-data-outcome-unknown', 'rejected':'smtp-data-rejected:450'}[failure]
                with self.assertRaisesRegex(SystemExit, expected): runpy.run_path(str(SCRIPT), run_name='__main__')
            elif reject:
                item = r'550/5\.1\.1' if enhanced else '550'
                expected = 'smtp-recipient-rejected:' + ','.join([item] * (6 if all_mx else 2))
                with self.assertRaisesRegex(SystemExit, expected):
                    runpy.run_path(str(SCRIPT), run_name='__main__')
            elif recipient != RECIPIENT:
                with self.assertRaises(SystemExit): runpy.run_path(str(SCRIPT), run_name='__main__')
            else:
                runpy.run_path(str(SCRIPT), run_name='__main__')
        return FakeSMTP.events

    def test_header_is_not_envelope_recipient(self):
        events = self.run_fixture()
        self.assertEqual([e[1] for e in events if e[0] == 'rcpt'], [RECIPIENT])
        self.assertEqual([e[1]['To'] for e in events if e[0] == 'data'], ['whlsxl@126.com'])

    def test_rejects_before_data_and_rejects_other_domains(self):
        events = self.run_fixture(reject=True)
        self.assertEqual(events[-1], ('rset',))
        self.assertFalse(any(e[0] == 'data' for e in events))
        self.assertEqual(self.run_fixture(recipient='other@example.test'), [])

    def test_connection_loss_before_and_after_data_are_distinct(self):
        self.assertFalse(any(e[0] == 'data' for e in self.run_fixture(failure='before')))
        self.assertTrue(any(e[0] == 'data' for e in self.run_fixture(failure='data')))
        self.assertTrue(any(e[0] == 'data' for e in self.run_fixture(failure='rejected')))

    def test_probe_never_submits_data(self):
        events = self.run_fixture(all_mx=True, reject_first=True, probe=True)
        self.assertEqual(sum(e[0] == 'rcpt' for e in events), 3)
        self.assertEqual(sum(e[0] == 'rset' for e in events), 3)
        self.assertFalse(any(e[0] == 'data' for e in events))
        events = self.run_fixture(all_mx=True, reject=True, probe=True)
        self.assertFalse(any(e[0] == 'data' for e in events))

    def test_other_mx_are_tried_only_before_a_single_data_submission(self):
        events = self.run_fixture(all_mx=True, reject_first=True)
        self.assertEqual(sum(e[0] == 'rcpt' for e in events), 3)
        self.assertEqual(sum(e[0] == 'rset' for e in events), 2)
        self.assertEqual(sum(e[0] == 'data' for e in events), 1)
        events = self.run_fixture(all_mx=True, reject=True)
        self.assertEqual(sum(e[0] == 'rcpt' for e in events), 6)
        self.assertFalse(any(e[0] == 'data' for e in events))
        self.run_fixture(all_mx=True, reject=True, enhanced=True)
        events = self.run_fixture(all_mx=True, reject_first=True, failure='data')
        self.assertEqual(sum(e[0] == 'data' for e in events), 1)


if __name__ == '__main__': unittest.main()
