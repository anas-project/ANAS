#!/usr/bin/env python3
# TEST_CASES: MAILMOD-T-010
"""One synthetic message to three isolated Worker routes through a SOCKS proxy."""
import json
import os
from pathlib import Path
import re
import smtplib
import socket
import ssl
import sys
from email.message import EmailMessage
from email.policy import SMTP
from email.utils import formatdate

if len(sys.argv) != 4:
    raise SystemExit('private binding, fixed MX and loopback SOCKS port required')
path, mx, port = Path(sys.argv[1]), sys.argv[2], sys.argv[3]
if not path.is_absolute() or path.is_symlink() or not path.is_file() or path.stat().st_uid != os.getuid() or path.stat().st_mode & 0o077 or path.stat().st_size > 16384:
    raise SystemExit('private isolated binding required')
if mx not in {'route1.mx.cloudflare.net', 'route2.mx.cloudflare.net', 'route3.mx.cloudflare.net'} or not re.fullmatch(r'[0-9]{1,5}', port) or not 1 <= int(port) <= 65535:
    raise SystemExit('unexpected SMTP target or proxy')
cfg = json.loads(path.read_text())
routes = cfg.get('Routes')
if not isinstance(routes, list) or len(routes) != 3:
    raise SystemExit('three isolated recipients required')
recipients = [r.get('address', '') for r in routes]
matches = [re.fullmatch(r'smtp-([a-f0-9]{12})-(main|alias|extra)@mail-poc\.bengbu\.cc\.cd', a) for a in recipients]
if any(m is None for m in matches) or len({m.group(1) for m in matches}) != 1 or [m.group(2) for m in matches] != ['main', 'alias', 'extra']:
    raise SystemExit('unexpected isolated recipients')
visible = cfg.get('VisibleTo', '')
source = cfg.get('Source', {}).get('ID', '')
if not re.fullmatch(r'[a-z0-9][a-z0-9._-]{0,63}@126\.com', visible) or not re.fullmatch(r'[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}', source) or cfg.get('Subject') != 'ANAS cloud SMTP ' + source:
    raise SystemExit('controlled visible address and subject required')

class SocksSMTP(smtplib.SMTP):
    def _get_socket(self, host, target_port, timeout):
        if host != mx or target_port != 25:
            raise OSError('unexpected SMTP target')
        sock = socket.create_connection(('127.0.0.1', int(port)), timeout)
        try:
            def exact(n):
                data = b''
                while len(data) < n:
                    part = sock.recv(n - len(data))
                    if not part:
                        raise OSError('SOCKS connection closed')
                    data += part
                return data
            sock.sendall(b'\x05\x01\x00')
            if exact(2) != b'\x05\x00':
                raise OSError('SOCKS authentication rejected')
            name = host.encode('ascii')
            sock.sendall(b'\x05\x01\x00\x03' + bytes([len(name)]) + name + b'\x00\x19')
            reply = exact(4)
            if reply[:3] != b'\x05\x00\x00':
                raise OSError('SOCKS connect rejected')
            if reply[3] == 1:
                exact(4)
            elif reply[3] == 4:
                exact(16)
            elif reply[3] == 3:
                exact(exact(1)[0])
            else:
                raise OSError('invalid SOCKS reply')
            exact(2)
            return sock
        except BaseException:
            sock.close()
            raise

message = EmailMessage(policy=SMTP)
message['From'] = 'probe@mail-poc.hlong.wang'
message['To'] = visible
message['Subject'] = cfg['Subject']
message['Date'] = formatdate(localtime=False)
message['Message-ID'] = '<anas-cloud-smtp-' + source + '@mail-poc.hlong.wang>'
message.set_content('ANAS real Cloudflare SMTP body')
message.add_attachment(bytes([0, 1, 2, 255]), maintype='application', subtype='octet-stream', filename='proof.bin')
with SocksSMTP(mx, 25, timeout=45) as client:
    if client.ehlo()[0] != 250 or not client.has_extn('starttls'):
        raise RuntimeError('SMTP TLS not offered')
    client.starttls(context=ssl.create_default_context())
    if client.ehlo()[0] != 250 or client.mail('probe@mail-poc.hlong.wang')[0] != 250:
        raise RuntimeError('SMTP sender rejected')
    codes = [client.rcpt(address)[0] for address in recipients]
    if codes != [250, 250, 250]:
        client.rset()
        raise RuntimeError('SMTP recipient rejected before DATA: ' + str(codes))
    code, _ = client.data(message.as_bytes())
    if code != 250:
        raise RuntimeError('SMTP DATA rejected: ' + str(code))
print(json.dumps({'provider': 'direct-mx', 'smtp_submission_accepted': True, 'data_code': code,
                  'recipients': len(recipients), 'visible_to_is_foreign': True}))
