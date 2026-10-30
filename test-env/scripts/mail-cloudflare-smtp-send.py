#!/usr/bin/env python3
# TEST_CASES: MAILMOD-T-010
"""One explicitly controlled SMTP transaction, with a misleading visible To."""
import json
from pathlib import Path
import re
import smtplib
import ssl
import sys
from email.message import EmailMessage
from email.policy import SMTP
from email.utils import formatdate


class RecordData:
    def data(self, message):
        self.data_result = super().data(message)
        return self.data_result


class RecordedSMTP(RecordData, smtplib.SMTP):
    pass


class RecordedSMTPS(RecordData, smtplib.SMTP_SSL):
    pass


key_path, input_path = map(Path, sys.argv[1:])
assert all(p.is_absolute() and p.is_file() and not p.is_symlink() and p.stat().st_mode & 0o077 == 0 for p in [key_path, input_path])
key, cfg = [json.loads(p.read_text()) for p in [key_path, input_path]]
assert key['domain'] == 'mail-poc.hlong.wang'
assert (key['provider'], key['smtp_host'], key['smtp_port'], key['smtp_username']) in [
    ('resend', 'smtp.resend.com', 587, 'resend'),
    ('eusend', 'smtp.eusend.dev', 465, 'eusend')]
assert len(cfg['Routes']) == 3
recipients = [r['address'] for r in cfg['Routes']]
assert all(re.fullmatch(r'smtp-[a-f0-9]+-(main|alias|extra)@(?:mail-poc\.bengbu\.cc\.cd|hlong\.wang)', a) for a in recipients)
message = EmailMessage(policy=SMTP)
message['From'] = 'probe@mail-poc.hlong.wang'
visible_to = cfg.get('VisibleTo', 'forged@foreign.example')
assert visible_to in ['forged@foreign.example', recipients[0]] or re.fullmatch(r'[a-z0-9][a-z0-9._-]{0,63}@126\.com', visible_to)
message['To'] = visible_to
message['Subject'] = cfg['Subject']
message['Date'] = formatdate(localtime=False)
message['Message-ID'] = '<anas-cloud-smtp-'+cfg['Source']['ID']+'@mail-poc.hlong.wang>'
message.set_content('ANAS real Cloudflare SMTP body')
message.add_attachment(bytes([0, 1, 2, 255]), maintype='application', subtype='octet-stream', filename='proof.bin')
client = (RecordedSMTPS(key['smtp_host'], 465, timeout=45, context=ssl.create_default_context())
          if key['smtp_port'] == 465 else RecordedSMTP(key['smtp_host'], 587, timeout=45))
with client:
    client.ehlo()
    if key['smtp_port'] == 587:
        client.starttls(context=ssl.create_default_context())
        client.ehlo()
    client.login(key['smtp_username'], key['api_key'])
    try:
        refused = client.sendmail('probe@mail-poc.hlong.wang', recipients, message.as_bytes())
        assert not refused, 'SMTP rejected a controlled envelope target'
        code, reply = client.data_result
    except smtplib.SMTPDataError as error:
        code, reply = error.smtp_code, error.smtp_error
    if code != 250:
        reason = reply.decode('utf-8', errors='replace').replace(key['api_key'], '[redacted]')
        reason = re.sub(r'[\x00-\x1f\x7f]+', ' ', reason)
        reason = re.sub(r'[A-Za-z0-9_/+=-]{32,}', '[redacted-id]', reason)
        raise RuntimeError('SMTP DATA rejected '+str(code)+': '+reason[:256])
    ids = re.findall(r'[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}', reply.decode('ascii', errors='replace'))
print(json.dumps({'provider': key['provider'], 'smtp_submission_accepted': True, 'data_code': code,
                  'queue_ids': ids, 'recipients': len(recipients),
                  'visible_to_is_foreign': visible_to != recipients[0]}))
