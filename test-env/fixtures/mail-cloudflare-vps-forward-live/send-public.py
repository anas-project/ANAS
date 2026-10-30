# TEST_CASES: MAILMOD-T-009
"""Submit one synthetic public test message from finance; no automatic retry."""
import json, pathlib, re, smtplib, ssl, sys
from email.message import EmailMessage
from email.policy import SMTP
from email.utils import formatdate

cfg=json.load(sys.stdin)
if set(cfg) != {'recipient','nonce'} or not re.fullmatch(r'cf-vps-test-[a-f0-9]{12}@mail-poc\.bengbu\.cc\.cd', cfg['recipient']) or not re.fullmatch(r'[a-f0-9]{32}',cfg['nonce']):
    raise SystemExit('invalid isolated recipient')
path=pathlib.Path('/home/whl/anas-mail-runtime-20261026/eusend-sending-key.json')
st=path.lstat()
if path.is_symlink() or not path.is_file() or st.st_uid != __import__('os').getuid() or st.st_mode & 0o077:
    raise SystemExit('private sending credential required')
key=json.loads(path.read_text())
if (key['provider'],key['domain'],key['smtp_host'],key['smtp_port'],key['smtp_username']) != ('eusend','mail-poc.hlong.wang','smtp.eusend.dev',465,'eusend'):
    raise SystemExit('isolated sender binding mismatch')
msg=EmailMessage(policy=SMTP)
msg['From']='probe@mail-poc.hlong.wang'
msg['To']='misleading@foreign.example'
msg['Subject']='ANAS isolated forwarding '+cfg['nonce']
msg['Date']=formatdate(localtime=False)
msg['Message-ID']='<anas-forward-'+cfg['nonce']+'@mail-poc.hlong.wang>'
msg.set_content('isolated-test-body '+cfg['nonce'])
msg.add_attachment(bytes([0,1,2,255]),maintype='application',subtype='octet-stream',filename='proof.bin')
try:
    with smtplib.SMTP_SSL(key['smtp_host'],465,timeout=45,context=ssl.create_default_context()) as client:
        client.ehlo();client.login(key['smtp_username'],key['api_key'])
        refused=client.sendmail('probe@mail-poc.hlong.wang',[cfg['recipient']],msg.as_bytes())
        if refused: raise RuntimeError('recipient refused')
except smtplib.SMTPDataError as exc:
    # Preserve only a bounded reply code. Provider text may include addresses
    # or other sensitive data. A negative DATA reply proves non-acceptance.
    if 400 <= exc.smtp_code <= 599:
        raise SystemExit('smtp-data-rejected:'+str(exc.smtp_code))
    raise SystemExit('synthetic submission unconfirmed; do not retry blindly')
except smtplib.SMTPRecipientsRefused:
    raise SystemExit('smtp-all-recipients-rejected-before-data')
except Exception:
    raise SystemExit('synthetic submission unconfirmed; do not retry blindly')
print('synthetic SMTP submission accepted; delivery not yet confirmed')
