# TEST_CASES: MAILMOD-T-009
"""One isolated public SMTP message through an explicitly supplied local SOCKS proxy."""
import json
import re
import smtplib
import socket
import ssl
import sys
import time
from email.message import EmailMessage
from email.policy import SMTP
from email.utils import formatdate

if len(sys.argv) not in (3, 4) or len(sys.argv) == 4 and sys.argv[3] != 'probe':
    raise SystemExit('fixed Cloudflare MX, loopback SOCKS port and optional probe required')
mx, port = sys.argv[1:3]
probe_only = len(sys.argv) == 4
mx_hosts = ['route1.mx.cloudflare.net', 'route2.mx.cloudflare.net', 'route3.mx.cloudflare.net']
if mx not in {*mx_hosts, 'all'} or not re.fullmatch(r'[0-9]{1,5}', port) or not 1 <= int(port) <= 65535:
    raise SystemExit('unexpected SMTP target or proxy')
cfg = json.load(sys.stdin)
if set(cfg) != {'recipient', 'nonce'} or not re.fullmatch(r'cf-vps-test-[a-f0-9]{12}@mail-poc\.bengbu\.cc\.cd', cfg['recipient']) or not re.fullmatch(r'[a-f0-9]{32}', cfg['nonce']):
    raise SystemExit('unexpected isolated recipient')


class SocksSMTP(smtplib.SMTP):
    def _get_socket(self, host, target_port, timeout):
        if host != mx or target_port != 25:
            raise OSError('unexpected SMTP target')
        sock = socket.create_connection(('127.0.0.1', int(port)), timeout)
        try:
            def exact(size):
                data = b''
                while len(data) < size:
                    chunk = sock.recv(size - len(data))
                    if not chunk: raise OSError('SOCKS connection closed')
                    data += chunk
                return data
            sock.sendall(b'\x05\x01\x00')
            if exact(2) != b'\x05\x00': raise OSError('SOCKS authentication rejected')
            name = host.encode('ascii')
            sock.sendall(b'\x05\x01\x00\x03' + bytes([len(name)]) + name + b'\x00\x19')
            reply = exact(4)
            if reply[:3] != b'\x05\x00\x00': raise OSError('SOCKS connect rejected')
            if reply[3] == 1: exact(4)
            elif reply[3] == 4: exact(16)
            elif reply[3] == 3: exact(exact(1)[0])
            else: raise OSError('invalid SOCKS reply')
            exact(2)
            return sock
        except BaseException:
            sock.close()
            raise


nonce = cfg['nonce']
msg = EmailMessage(policy=SMTP)
msg['From'] = 'probe@mail-poc.hlong.wang'
msg['To'] = 'whlsxl@126.com'
msg['Subject'] = 'ANAS isolated forwarding ' + nonce
msg['Date'] = formatdate(localtime=False)
msg['Message-ID'] = '<anas-forward-' + nonce + '@mail-poc.hlong.wang>'
msg.set_content('isolated-test-body ' + nonce)
msg.add_attachment(bytes([0, 1, 2, 255]), maintype='application', subtype='octet-stream', filename='proof.bin')
stage = 'before-data'
targets = mx_hosts if mx == 'all' else [mx]
rejections = []
try:
    for attempt in range(2):
        accepted = False
        for mx in targets:
            with SocksSMTP(mx, 25, timeout=15) as client:
                if client.ehlo()[0] != 250 or not client.has_extn('starttls'):
                    raise RuntimeError('SMTP TLS not offered')
                client.starttls(context=ssl.create_default_context())
                if client.ehlo()[0] != 250 or client.mail('probe@mail-poc.hlong.wang')[0] != 250:
                    raise RuntimeError('SMTP sender rejected')
                recipient_code, reply = client.rcpt(cfg['recipient'])
                if recipient_code != 250:
                    client.rset()
                    if not 400 <= recipient_code <= 599: raise RuntimeError('SMTP recipient response unconfirmed')
                    enhanced = re.match(rb'^\s*([45]\.[0-9]{1,3}\.[0-9]{1,3})(?:\s|$)', reply)
                    rejections.append(str(recipient_code) + ('/' + enhanced.group(1).decode('ascii') if enhanced else ''))
                    continue
                if probe_only:
                    client.rset()
                    accepted = True
                    break
                stage = 'data-attempted'
                try:
                    code, _ = client.data(msg.as_bytes())
                except smtplib.SMTPDataError as exc:
                    if 400 <= exc.smtp_code <= 599: raise SystemExit('smtp-data-rejected:' + str(exc.smtp_code))
                    raise
                if code != 250:
                    if 400 <= code <= 599: raise SystemExit('smtp-data-rejected:' + str(code))
                    raise RuntimeError('SMTP DATA not accepted')
                accepted = True
            if accepted: break
        if accepted: break
        if attempt == 0: time.sleep(15)
    if not accepted:
        raise SystemExit('smtp-recipient-rejected:' + ','.join(rejections))
except Exception:
    # Never print server text, addresses, or exceptions. Once DATA is attempted,
    # a lost response or failed QUIT cannot be treated as non-acceptance.
    raise SystemExit('smtp-pre-data-failed' if stage == 'before-data' else 'smtp-data-outcome-unknown')
print('synthetic SMTP recipient preflight accepted; no DATA sent' if probe_only else 'synthetic direct SMTP DATA accepted')
