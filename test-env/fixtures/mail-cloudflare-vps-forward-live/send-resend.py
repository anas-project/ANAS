# TEST_CASES: MAILMOD-T-009
"""Submit one isolated test message with finance's scoped Resend credential."""
import json
import os
import pathlib
import re
import smtplib
import ssl
import sys
from email.message import EmailMessage
from email.policy import SMTP
from email.utils import formatdate

cfg = json.load(sys.stdin)
if set(cfg) != {'recipient', 'nonce'} or not re.fullmatch(r'cf-vps-test-[a-f0-9]{12}@mail-poc\.bengbu\.cc\.cd', cfg['recipient']) or not re.fullmatch(r'[a-f0-9]{32}', cfg['nonce']):
    raise SystemExit('invalid isolated recipient')
path = pathlib.Path('/home/whl/anas-mail-runtime-20261026/resend-sending-key.json')
st = path.lstat()
if path.is_symlink() or not path.is_file() or st.st_uid != os.getuid() or st.st_mode & 0o077:
    raise SystemExit('private sending credential required')
key = json.loads(path.read_text())
if (key['provider'], key['domain'], key['smtp_host'], key['smtp_port'], key['smtp_username']) != ('resend', 'mail-poc.hlong.wang', 'smtp.resend.com', 587, 'resend'):
    raise SystemExit('isolated sender binding mismatch')
nonce = cfg['nonce']
msg = EmailMessage(policy=SMTP)
msg['From'] = 'probe@mail-poc.hlong.wang'
msg['To'] = 'whlsxl@126.com'
msg['Subject'] = 'ANAS isolated forwarding ' + nonce
msg['Date'] = formatdate(localtime=False)
msg['Message-ID'] = '<anas-forward-' + nonce + '@mail-poc.hlong.wang>'
msg.set_content('isolated-test-body ' + nonce)
msg.add_attachment(bytes([0, 1, 2, 255]), maintype='application', subtype='octet-stream', filename='proof.bin')
try:
    with smtplib.SMTP(key['smtp_host'], 587, timeout=45) as client:
        client.ehlo()
        client.starttls(context=ssl.create_default_context())
        client.ehlo()
        client.login(key['smtp_username'], key['api_key'])
        refused = client.sendmail('probe@mail-poc.hlong.wang', [cfg['recipient']], msg.as_bytes())
        if refused: raise RuntimeError('recipient refused')
except smtplib.SMTPDataError as exc:
    if 400 <= exc.smtp_code <= 599: raise SystemExit('smtp-data-rejected:' + str(exc.smtp_code))
    raise SystemExit('synthetic submission unconfirmed; do not retry blindly')
except smtplib.SMTPRecipientsRefused:
    raise SystemExit('smtp-all-recipients-rejected-before-data')
except Exception:
    raise SystemExit('synthetic submission unconfirmed; do not retry blindly')
print('synthetic SMTP submission accepted; delivery not yet confirmed')
