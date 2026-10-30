#!/usr/bin/env python3
# TEST_CASES: MAILMOD-T-010
"""Read-only SMTP envelope probe for the three disposable Worker addresses.

Never issues DATA, supplies credentials, or sends message content. The caller
separately verifies the public rule, then runs the real authorized SMTP test.
"""
import json
import os
import re
import smtplib
import socket
import ssl
import sys
from pathlib import Path

if len(sys.argv) not in (2, 3, 4):
    raise SystemExit('unexpected probe arguments')
path = Path(sys.argv[1])
mx = sys.argv[2] if len(sys.argv) >= 3 else 'route2.mx.cloudflare.net'
if mx not in {'route1.mx.cloudflare.net', 'route2.mx.cloudflare.net', 'route3.mx.cloudflare.net'}:
    raise SystemExit('unexpected Cloudflare MX host')
if not path.is_absolute() or path.is_symlink() or not path.is_file() or path.stat().st_uid != os.getuid() or path.stat().st_mode & 0o077 or path.stat().st_size > 16384:
    raise SystemExit('private isolated binding required')
cfg = json.loads(path.read_text())
routes = cfg.get('Routes')
if not isinstance(routes, list) or len(routes) != 3:
    raise SystemExit('three isolated recipients required')
recipients = [r.get('address', '') for r in routes]
matches = [re.fullmatch(r'smtp-([a-f0-9]{12})-(main|alias|extra)@(?:mail-poc\.bengbu\.cc\.cd|hlong\.wang)', a) for a in recipients]
if any(m is None for m in matches) or len({m.group(1) for m in matches}) != 1 or [m.group(2) for m in matches] != ['main', 'alias', 'extra']:
    raise SystemExit('unexpected isolated recipient')


class IPv4SMTP(smtplib.SMTP):
    def _get_socket(self, host, port, timeout):
        addresses = socket.getaddrinfo(host, port, socket.AF_INET, socket.SOCK_STREAM)
        if not addresses:
            raise OSError('SMTP MX IPv4 unavailable')
        return socket.create_connection(addresses[0][4], timeout, self.source_address)


class SocksSMTP(smtplib.SMTP):
    def _get_socket(self, host, port, timeout):
        if host != mx or port != 25:
            raise OSError('unexpected SMTP target')
        sock = socket.create_connection(('127.0.0.1', socks_port), timeout)
        try:
            def read_exact(n):
                data = b''
                while len(data) < n:
                    part = sock.recv(n - len(data))
                    if not part:
                        raise OSError('SOCKS connection closed')
                    data += part
                return data
            sock.sendall(b'\x05\x01\x00')
            if read_exact(2) != b'\x05\x00':
                raise OSError('SOCKS authentication rejected')
            name = host.encode('ascii')
            sock.sendall(b'\x05\x01\x00\x03' + bytes([len(name)]) + name + b'\x00\x19')
            response = read_exact(4)
            if response[:3] != b'\x05\x00\x00':
                raise OSError('SOCKS connection rejected')
            address_type = response[3]
            if address_type == 1:
                read_exact(4)
            elif address_type == 4:
                read_exact(16)
            elif address_type == 3:
                read_exact(read_exact(1)[0])
            else:
                raise OSError('invalid SOCKS address type')
            read_exact(2)
            return sock
        except BaseException:
            sock.close()
            raise


socks_port = None
if len(sys.argv) == 4:
    if not re.fullmatch(r'[0-9]{1,5}', sys.argv[3]) or not 1 <= int(sys.argv[3]) <= 65535:
        raise SystemExit('invalid loopback SOCKS port')
    socks_port = int(sys.argv[3])
result = {'phase': 'connect', 'codes': []}
client = None
try:
    client = (SocksSMTP if socks_port else IPv4SMTP)(mx, 25, timeout=12)
    if client.ehlo()[0] != 250 or not client.has_extn('starttls'):
        raise RuntimeError('SMTP TLS not offered')
    client.starttls(context=ssl.create_default_context())
    if client.ehlo()[0] != 250:
        raise RuntimeError('SMTP EHLO rejected after TLS')
    result['phase'] = 'mail'
    if client.mail('probe@mail-poc.hlong.wang')[0] != 250:
        raise RuntimeError('SMTP probe sender rejected')
    result['phase'] = 'rcpt'
    for address in recipients:
        result['codes'].append(client.rcpt(address)[0])
    client.rset()
    result['phase'] = 'complete'
except (OSError, smtplib.SMTPException, RuntimeError) as error:
    result['error'] = type(error).__name__
finally:
    if client:
        try:
            client.quit()
        except (OSError, smtplib.SMTPException):
            client.close()
print(json.dumps(result))
if result['phase'] != 'complete' or result['codes'] != [250, 250, 250]:
    raise SystemExit(1)
