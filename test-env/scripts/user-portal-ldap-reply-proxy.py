#!/usr/bin/env python3
# TEST_CASES: PORTAL-T-003
"""Isolated fault probe: forward LDAP over verified TLS, lose one Modify reply.

Payloads remain in memory and are never logged. This is not a portal component.
"""
import json
import os
from pathlib import Path
import socket
import ssl
import threading
from samba.param import LoadParm

if not os.environ.get('PORTAL_TEST_FIXTURE', '').startswith('anas-user-portal-e2e-test-'):
    raise SystemExit('isolated fixture marker required')
lp = LoadParm()
lp.load('/etc/samba/smb.conf')
server = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
server.minimum_version = ssl.TLSVersion.TLSv1_2
server.load_cert_chain(lp.get('tls certfile'), lp.get('tls keyfile'))
upstream = ssl.create_default_context(cafile='/certs/anas-trust-bundle.crt')
upstream.minimum_version = ssl.TLSVersion.TLSv1_2
server_name = os.environ['SAMBA_DC_DC_DOMAIN']
marker = Path('/tmp/anas-portal-drop-next-reply')
evidence = Path('/tmp/anas-portal-proxy-result.json')
lock = threading.Lock()
counts = {'modify_requests': 0, 'saved_dropped': 0}

def exact(conn, length):
    data = bytearray()
    while len(data) < length:
        piece = conn.recv(length-len(data))
        if not piece:
            raise EOFError()
        data.extend(piece)
    return bytes(data)

def frame(conn):
    head = exact(conn, 2)
    if head[0] != 0x30:
        raise ValueError('LDAP frame required')
    if head[1] & 128:
        width = head[1] & 127
        if width < 1 or width > 4:
            raise ValueError('invalid frame length')
        tail = exact(conn, width)
        length = int.from_bytes(tail, 'big')
        head += tail
    else:
        length = head[1]
    if length > 1 << 20:
        raise ValueError('frame capacity exceeded')
    return head + exact(conn, length)

def element(data, offset):
    tag = data[offset]
    width = data[offset+1]
    if width & 128:
        count = width & 127
        length = int.from_bytes(data[offset+2:offset+2+count], 'big')
        start = offset+2+count
    else:
        length = width
        start = offset+2
    return tag, start, start+length

def operation(data):
    _, start, _ = element(data, 0)
    _, _, end = element(data, start)
    return element(data, end)

def relay(raw):
    try:
        with server.wrap_socket(raw, server_side=True) as client:
            client.settimeout(15)
            with upstream.wrap_socket(socket.create_connection(('127.0.0.1', 636), 10), server_hostname=server_name) as target:
                target.settimeout(15)
                while True:
                    request = frame(client)
                    tag, _, _ = operation(request)
                    if tag == 0x42:  # Unbind has no response.
                        target.sendall(request)
                        return
                    drop = False
                    if tag == 0x66:
                        with lock:
                            counts['modify_requests'] += 1
                            if marker.exists():
                                marker.unlink()
                                drop = True
                    target.sendall(request)
                    while True:
                        response = frame(target)
                        response_tag, start, _ = operation(response)
                        if drop and response_tag == 0x67:
                            _, value, end = element(response, start)
                            code = int.from_bytes(response[value:end], 'big')
                            with lock:
                                counts['ldap_code'] = code
                                counts['saved_dropped'] += int(code == 0)
                                evidence.write_text(json.dumps(counts))
                                evidence.chmod(0o600)
                            return
                        client.sendall(response)
                        if response_tag not in (0x64, 0x73):
                            break
    except (OSError, EOFError, ValueError):
        raw.close()

listener = socket.socket()
listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
listener.bind(('0.0.0.0', 1636))
listener.listen(16)
while True:
    conn, _ = listener.accept()
    threading.Thread(target=relay, args=(conn,), daemon=True).start()
