#!/usr/bin/env python3
# TEST_CASES: MAILMOD-T-007
"""Disposable fixed-DMS Milter/LMTP observation, never a delivery attestation.

No third-party dependency or product entrypoint. The observation mode relays
Rspamd unchanged. The opt-in copy experiment negotiates exact header whitespace
and persists reconstructed copies. Neither mode issues product or cloud ACKs.
"""
import email
import hashlib
import json
import os
import pwd
import re
import secrets
import smtplib
import socket
import struct
import subprocess
import threading
import time
from pathlib import Path

BOUND_COPY = os.environ.get('MAIL_DMS_BOUND_COPY_PROBE') == '1'
if BOUND_COPY:
    from copy_binding import reconstruct, split_message
    from copy_store import CopyStore

if os.environ.get('MAIL_DMS_MILTER_PROBE') != '1' or not Path('/input/native.json').is_file():
    raise SystemExit('explicit disposable DMS fixture required')
USER = 'mbx_' + 'a' * 32 + '@mailboxes.anas.invalid'
HEADER = Path('/etc/postfix/anas-intake-trusted-headers')
ORIGINAL = b'/^X-ANAS-Ingress-Key: [a-f0-9]{64}$/ WARN ANAS ingress trace\n'
MILTER = Path('/var/spool/postfix/private/anas-milter-observation.sock')
LMTP = Path('/var/spool/postfix/private/anas-milter-lmtp-observation.sock')
RSPAMD = Path('/etc/rspamd/override.d/milter_headers.conf')
SIEVE = Path('/var/mail-state/anas-milter-observation.sieve')
SIEVE_CONF = Path('/etc/dovecot/conf.d/98-anas-milter-observation.conf')
SIEVE_BIN = SIEVE.with_suffix('.svbin')
if HEADER.read_bytes() != ORIGINAL or any(p.exists() or p.is_symlink() for p in [MILTER, LMTP, SIEVE, SIEVE_CONF, SIEVE_BIN]) or RSPAMD.is_symlink():
    raise SystemExit('disposable fixture configuration conflict')
stop = threading.Event()
failures = []
records = {}
received = {}
expected = {}
threads = []
listeners = []
active = []
intent_received = {}
proof_root = Path('/var/mail-state/anas-bound-copy-probe')
proof_store = None
lock = threading.Lock()
postfix = pwd.getpwnam('postfix')


def cmd(*args):
    p = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
    if p.returncode:
        raise RuntimeError('native command failed: ' + args[0])
    return p.stdout.decode().strip()


def spawn(fn, *args):
    def checked():
        try:
            fn(*args)
        except BaseException as exc:
            if not stop.is_set():
                failures.append(type(exc).__name__ + ': ' + str(exc))
    t = threading.Thread(target=checked, daemon=True)
    threads.append(t)
    t.start()
    return t


def exact(conn, size):
    data = bytearray()
    while len(data) < size:
        chunk = conn.recv(size - len(data))
        if not chunk:
            if not data:
                return None
            raise RuntimeError('truncated Milter frame')
        data.extend(chunk)
    return bytes(data)


def frame(conn):
    size = exact(conn, 4)
    if size is None:
        return None
    n, = struct.unpack('!I', size)
    if not 1 <= n <= 1 << 20:
        raise RuntimeError('unbounded Milter frame')
    payload = exact(conn, n)
    if payload is None:
        raise RuntimeError('missing Milter frame')
    return size + payload


def packed(tag, body):
    return struct.pack('!I', len(body) + 1) + tag + body


def bound_delivery(key, data):
    def send(target, delivered):
        try:
            with smtplib.LMTP('/var/run/dovecot/lmtp', timeout=20) as dovecot:
                return 451 if dovecot.sendmail('', [target], delivered) else 250
        except (OSError, smtplib.SMTPException):
            return 451
    result = proof_store.deliver(key, USER, data, send)
    if result == 250:
        received[key] = data
    return (str(result) + ' bounded copy result\r\n').encode()


def milter_session(client):
    state = {'headers': [], 'body': bytearray(), 'responses': [], 'recipients': [], 'key': None}
    with client, socket.create_connection(upstream, timeout=25) as server:
        active.extend([client, server])
        client.settimeout(35)
        server.settimeout(35)

        def responses():
            while not stop.is_set():
                try:
                    packet = frame(server)
                except OSError:
                    if state.get('closing'):
                        return
                    raise
                if packet is None:
                    return
                tag, body = packet[4:5], packet[5:]
                with lock:
                    if tag == b'O':
                        if len(body) != 12:
                            raise RuntimeError('unexpected negotiation')
                        state['negotiation'] = struct.unpack('!III', body)
                        if BOUND_COPY:
                            version, actions, flags = state['negotiation']
                            # Preserve the input's exact first space. Adapt
                            # upstream requests/responses to its own negotiation.
                            packet = packed(tag, struct.pack('!III', version, actions, flags | (1 << 20)))
                    elif tag in [b'h', b'i', b'm', b'b', b'e', b'+', b'-', b'q']:
                        state['responses'].append((tag.decode(), body))
                        if BOUND_COPY and tag in [b'h', b'i', b'm'] and not state['negotiation'][2] & (1 << 20):
                            prefix, fields = (body[:4], body[4:]) if tag in [b'i', b'm'] else (b'', body)
                            parts = fields.split(b'\0')
                            if len(parts) != 3 or parts[-1]:
                                raise RuntimeError('invalid upstream header modification')
                            if parts[1] or tag != b'm':
                                parts[1] = b' ' + parts[1]
                            packet = packed(tag, prefix + b'\0'.join(parts))
                    elif tag in [b'c', b'a', b'r', b't', b'd', b'y'] and state.get('eom'):
                        state['verdict'] = tag.decode()
                        if state['key']:
                            if BOUND_COPY and tag in [b'c', b'a']:
                                key = state['key']
                                if key not in expected or state['recipients'] != [('<'+USER+'>').encode()]:
                                    raise RuntimeError('filter input target mismatch')
                                rebuilt = reconstruct(expected[key], state['headers'], bytes(state['body']), state['responses'], upstream_leading_space=bool(state['negotiation'][2] & (1 << 20)))
                                proof_store.prepare(key, USER, rebuilt, intent_received[key])
                            records[state['key']] = state
                client.sendall(packet)

        worker = spawn(responses)
        while not stop.is_set():
            packet = frame(client)
            if packet is None:
                break
            tag, body = packet[4:5], packet[5:]
            with lock:
                if tag == b'M':
                    state = {'headers': [], 'body': bytearray(), 'responses': [], 'recipients': [], 'sender': body.split(b'\0')[0], 'key': None, 'negotiation': state['negotiation']}
                elif tag == b'L':
                    parts = body.split(b'\0')
                    if len(parts) != 3 or parts[-1] or sum(len(v) for v in state['headers']) > 262144:
                        raise RuntimeError('ambiguous header frame')
                    state['headers'].append(body)
                    if parts[0].lower() == b'x-anas-ingress-key':
                        if state['key']:
                            raise RuntimeError('duplicate probe key')
                        state['key'] = parts[1].strip().decode('ascii')
                    if BOUND_COPY and not state['negotiation'][2] & (1 << 20) and parts[1].startswith(b' '):
                        packet = packed(tag, parts[0] + b'\0' + parts[1][1:] + b'\0')
                elif tag == b'B':
                    state['body'].extend(body)
                    if len(state['body']) > 1 << 20:
                        raise RuntimeError('probe message too large')
                elif tag == b'R':
                    state['recipients'].append(body.split(b'\0')[0])
                elif tag == b'E':
                    state['eom'] = True
                    if BOUND_COPY:
                        key = state['key']
                        if key not in expected or state['sender'] != b'<>' or state['recipients'] != [('<'+USER+'>').encode()]:
                            raise RuntimeError('untrusted original or target')
                        reconstruct(expected[key], state['headers'], bytes(state['body']), [])
            server.sendall(packet)
            if tag == b'Q':
                break
        state['closing'] = True
        try:
            server.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        worker.join(2)


def lmtp_session(conn):
    with conn, conn.makefile('rwb', buffering=0) as f:
        active.append(conn)
        conn.settimeout(30)
        pid, uid, _ = struct.unpack('3i', conn.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        if uid != postfix.pw_uid or pid < 1:
            raise RuntimeError('unexpected LMTP peer')
        f.write(b'220 isolated LMTP observation\r\n')
        target = None
        while True:
            row = f.readline(4097)
            if not row:
                return
            if len(row) > 4096:
                raise RuntimeError('long LMTP command')
            op = row.upper()
            if op.startswith(b'LHLO '):
                f.write(b'250 isolated\r\n')
            elif op == b'MAIL FROM:<>\r\n':
                target = None
                f.write(b'250 sender\r\n')
            elif op.startswith(b'RCPT TO:') and target is None and row.strip().decode().lower() == 'rcpt to:<' + USER + '>':
                target = USER
                f.write(b'250 recipient\r\n')
            elif op == b'DATA\r\n' and target == USER:
                f.write(b'354 continue\r\n')
                data = bytearray()
                while len(data) <= 1 << 20:
                    line = f.readline(65537)
                    if not line or len(line) > 65536:
                        raise RuntimeError('invalid synthetic DATA')
                    if line == b'.\r\n':
                        break
                    data.extend(line[1:] if line.startswith(b'..') else line)
                else:
                    raise RuntimeError('large synthetic DATA')
                message = email.message_from_bytes(data)
                key = message.get('X-ANAS-Ingress-Key', '')
                if BOUND_COPY:
                    if key not in expected:
                        f.write(b'550 unknown private intent\r\n')
                    else:
                        f.write(bound_delivery(key, bytes(data)))
                    continue
                if key not in expected or key in received:
                    raise RuntimeError('unknown or repeated synthetic intent')
                delivered = bytes(data)
                with smtplib.LMTP('/var/run/dovecot/lmtp', timeout=20) as dovecot:
                    if dovecot.sendmail('', [USER], delivered):
                        raise RuntimeError('native Dovecot refused synthetic message')
                received[key] = bytes(data)
                f.write(b'250 synthetic LMTP completed\r\n')
            elif op == b'QUIT\r\n':
                f.write(b'221 bye\r\n')
                return
            elif op == b'RSET\r\n':
                target = None
                f.write(b'250 reset\r\n')
            else:
                f.write(b'550 unsupported probe command\r\n')


def listener(path, handler):
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.bind(str(path))
    os.chown(path, 0, postfix.pw_gid)
    os.chmod(path, 0o660)
    sock.listen(8)
    sock.settimeout(.5)
    listeners.append(sock)

    def accept():
        while not stop.is_set():
            try:
                client, _ = sock.accept()
            except socket.timeout:
                continue
            spawn(handler, client)
    spawn(accept)


def same_uid_delivery(data):
    code = '''import smtplib,sys
try:
 with smtplib.LMTP(sys.argv[1],timeout=15) as c:
  result=c.sendmail('',[sys.argv[2]],sys.stdin.buffer.read())
  print(next(iter(result.values()))[0] if result else 250)
except smtplib.SMTPResponseException as e:print(e.smtp_code)
'''
    p = subprocess.run(['python3', '-c', code, str(LMTP), USER], input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE, user=postfix.pw_uid, group=postfix.pw_gid, extra_groups=[], timeout=20)
    if p.returncode or p.stdout.strip() not in [b'250', b'451', b'550']:
        raise RuntimeError('same-UID LMTP test did not produce a bounded result')
    return int(p.stdout.strip())


native_milters = cmd('/usr/sbin/postconf', '-xh', 'smtpd_milters')
match = re.fullmatch(r'inet:(?:127\.0\.0\.1|localhost):([0-9]+)', native_milters)
if not match or cmd('/usr/sbin/postconf', '-xh', 'content_filter'):
    raise SystemExit('only the fixed single-Rspamd chain is supported by this probe')
upstream = ('127.0.0.1', int(match.group(1)))
parameter = '127.0.0.1:2525/inet/smtpd_milters'
failure_parameter = '127.0.0.1:2525/inet/milter_default_action'
prior_override = subprocess.run(['/usr/sbin/postconf', '-P', parameter], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, check=False).stdout
prior_failure = subprocess.run(['/usr/sbin/postconf', '-P', failure_parameter], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, check=False).stdout
def matches_override(raw, name, expected):
    key, separator, value = raw.decode().strip().partition('=')
    return separator and key.strip() == name and value.strip() == expected

if prior_override and not matches_override(prior_override, parameter, native_milters) or prior_failure and not matches_override(prior_failure, failure_parameter, 'tempfail'):
    raise SystemExit('unrecognized private Milter override')
rspamd_original = RSPAMD.read_bytes() if RSPAMD.exists() else None
rspamd_mode = RSPAMD.stat().st_mode & 0o777 if RSPAMD.exists() else 0o644
try:
    if BOUND_COPY:
        if proof_root.exists():
            raise RuntimeError('private proof fixture already exists')
        proof_store = CopyStore(proof_root)
    listener(MILTER, milter_session)
    listener(LMTP, lmtp_session)
    RSPAMD.write_text('use = ["x-spamd-result", "x-rspamd-action"];\nskip_local = false;\nskip_authenticated = false;\nextended_spam_headers = true;\n')
    os.chmod(RSPAMD, 0o644)
    cmd('/usr/bin/rspamadm', 'configtest')
    cmd('/usr/bin/supervisorctl', 'restart', 'rspamd')
    until = time.monotonic() + 60
    while True:
        try:
            with socket.create_connection(upstream, timeout=1):
                break
        except OSError:
            if time.monotonic() >= until:
                raise RuntimeError('Rspamd did not open its Milter socket')
            time.sleep(.2)
    SIEVE.write_text('if header :is "Subject" "milter-probe-discard" { discard; stop; }\n')
    os.chmod(SIEVE, 0o600)
    os.chown(SIEVE, 5000, 5000)
    SIEVE_CONF.write_text('sieve_script anas_milter_observation {\n type = before\n path = ' + str(SIEVE) + '\n}\n')
    os.chmod(SIEVE_CONF, 0o644)
    cmd('/usr/bin/sievec', str(SIEVE))
    os.chown(SIEVE_BIN, 5000, 5000)
    cmd('/usr/sbin/dovecot', 'reload')
    HEADER.write_bytes(b'/^X-ANAS-Ingress-Key: [a-f0-9]{64}$/ FILTER lmtp:unix:private/anas-milter-lmtp-observation.sock\n')
    cmd('/usr/sbin/postconf', '-P', parameter + '=unix:' + str(MILTER))
    # The upstream image defaults to accepting mail when a Milter is down.
    # This observation must never proceed on an unscanned synthetic message.
    cmd('/usr/sbin/postconf', '-P', failure_parameter + '=tempfail')
    cmd('/usr/sbin/postfix', 'reload')
    time.sleep(1)
    for label in ['normal', 'folded', 'discard', 'mime', 'gtube']:
        key = secrets.token_hex(32)
        content = ('From: probe@example.test\r\nTo: ' + USER + '\r\nX-ANAS-Ingress-Key: ' + key + '\r\nSubject: milter-probe-' + label + '\r\n').encode()
        if BOUND_COPY:
            content += ('Date: Thu, 01 Jan 2026 00:00:00 +0000\r\nMessage-ID: <'+key+'@example.test>\r\nReceived: from original.example.test\r\n').encode()
        if label == 'folded':
            content += b'X-Probe-Duplicate:\tfirst\r\n\tcontinued\r\nX-Probe-Duplicate: second\r\n'
        if label == 'mime':
            content += b'MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary="milter-probe"\r\n\r\n--milter-probe\r\nContent-Type: text/plain\r\n\r\n.dot-stuffed synthetic body\r\n--milter-probe\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\nAAEC/f7/\r\n--milter-probe--\r\n'
        elif label == 'gtube':
            content += b'\r\nXJS*C4JDBQADN1.NSBN3*2IDNEN*GTUBE-STANDARD-ANTI-UBE-TEST-EMAIL*C.34X\r\n'
        else:
            content += b'\r\nSynthetic body.\r\n'
        expected[key] = content
        intent_received[key] = ('Received: by anas-controlled-intake ('+key+'); '+email.utils.formatdate(usegmt=True)).encode()
        code = 250
        try:
            with smtplib.SMTP('127.0.0.1', 2525, timeout=30) as smtp:
                smtp.ehlo('localhost.localdomain')
                if smtp.sendmail('', [USER], content):
                    raise RuntimeError('unexpected recipient refusal')
        except smtplib.SMTPDataError as exc:
            code = exc.smtp_code
        until = time.monotonic() + 30
        while not failures and (key not in records or label != 'gtube' and key not in received) and time.monotonic() < until:
            time.sleep(.1)
        if failures:
            raise RuntimeError('; '.join(failures))
        if key not in records:
            raise RuntimeError('real Rspamd response unobserved')
        observation = records[key]
        if label == 'gtube':
            if code < 500 or key in received or observation.get('verdict') not in ['r', 'y']:
                raise RuntimeError('GTUBE did not fail before LMTP')
            print('PASS: original Rspamd rejected GTUBE before LMTP', flush=True)
            continue
        if code != 250 or key not in received:
            raise RuntimeError('synthetic LMTP completion unobserved')
        target_message = email.message_from_bytes(received[key])
        matching = cmd('/usr/bin/doveadm', 'search', '-u', USER, 'HEADER', 'X-ANAS-Ingress-Key', key)
        if bool(matching) != (label != 'discard'):
            raise RuntimeError('Sieve final state mismatch')
        header_names = [block.split(b'\0')[0].decode().lower() for block in observation['headers']]
        changes = [tag for tag, _ in observation['responses']]
        rspamd_headers = target_message.get_all('X-Spamd-Result', [])
        if not rspamd_headers or not any(tag in changes for tag in ['h', 'i']):
            raise RuntimeError('actual filter header modification unobserved')
        body = received[key].partition(b'\r\n\r\n')[2]
        summary = {'milter_received_count': header_names.count('received'),
                   'lmtp_received_count': len(target_message.get_all('Received', [])),
                   'actual_rspamd_header_changes': changes,
                   'rspamd_result_present': bool(rspamd_headers),
                   'milter_body_equals_lmtp_body': bytes(observation['body']) == body,
                   'original_body_equals_lmtp_body': content.partition(b'\r\n\r\n')[2] == body,
                   'upstream_header_leading_space_negotiated': bool(observation['negotiation'][2] & (1 << 20)),
                   'postfix_header_leading_space_requested': BOUND_COPY or bool(observation['negotiation'][2] & (1 << 20)),
                   'original_sha256': hashlib.sha256(content).hexdigest(),
                   'lmtp_sha256': hashlib.sha256(received[key]).hexdigest(),
                   'sieve_stored': bool(matching)}
        print('OBSERVATION: ' + label + ' ' + json.dumps(summary, sort_keys=True), flush=True)
        if not BOUND_COPY and (summary['milter_received_count'] or summary['lmtp_received_count'] != 1):
            raise RuntimeError('expected missing Received boundary not reproduced')
        if BOUND_COPY:
            saved = (proof_root/key/'delivery.eml').read_bytes()
            if b'Received: by anas-controlled-intake ' not in saved or b'Received: from original.example.test' not in saved:
                raise RuntimeError('private reconstruction lost original trace')
            print('BOUND COPY: '+label+' verified input, filter edits, durable copy and native processing', flush=True)
            if label == 'normal':
                transport = received[key]
                headers, original_body = split_message(transport)
                mutations = [
                    b'\r\n'.join(headers)+b'\r\n\r\nchanged body\r\n',
                    transport.replace(b'Subject: milter-probe-normal', b'Subject: milter-probe-discard'),
                    transport.replace(b'Received: from original.example.test', b'Received: from forged.example.test'),
                    b'Received: extra unverified hop\r\n'+transport,
                    b'X-Unverified: injected\r\n'+transport,
                ]
                for forged in mutations:
                    if same_uid_delivery(forged) != 550:
                        raise RuntimeError('same-UID forged content was not refused')
                for _ in range(2):
                    if same_uid_delivery(transport) != 250:
                        raise RuntimeError('verified completed replay not acknowledged')
                completed = proof_root/key/'completed'
                held = proof_root/key/'completion-held'
                completed.rename(held)
                try:
                    if same_uid_delivery(transport) != 451:
                        raise RuntimeError('missing durable completion triggered redelivery')
                finally:
                    held.rename(completed)
                matches = cmd('/usr/bin/doveadm', 'search', '-u', USER, 'HEADER', 'X-ANAS-Ingress-Key', key).splitlines()
                if len(matches) != 1:
                    raise RuntimeError('replay or uncertainty caused duplicate delivery')
                print('BOUND COPY: same-UID body/header/extra-header changes rejected; completed replay deduplicated; missing completion remains unknown', flush=True)
    print('EXPERIMENT COMPLETE: bound-copy normal-path stage only; security and crash matrix remain outstanding' if BOUND_COPY else 'EXPERIMENT COMPLETE: Milter transcript omits the final Received header; complete-content attestation remains blocked', flush=True)
finally:
    HEADER.write_bytes(ORIGINAL)
    for key, prior in [(parameter, prior_override), (failure_parameter, prior_failure)]:
        args = ['-P', prior.decode().strip()] if prior else ['-PX', key]
        subprocess.run(['/usr/sbin/postconf', *args], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
    if rspamd_original is None:
        RSPAMD.unlink(missing_ok=True)
    else:
        RSPAMD.write_bytes(rspamd_original)
        os.chmod(RSPAMD, rspamd_mode)
    for path in [SIEVE_CONF, SIEVE, SIEVE_BIN]:
        path.unlink(missing_ok=True)
    cmd('/usr/bin/supervisorctl', 'restart', 'rspamd')
    cmd('/usr/sbin/dovecot', 'reload')
    cmd('/usr/sbin/postfix', 'reload')
    stop.set()
    for conn in active + listeners:
        try:
            conn.close()
        except OSError:
            pass
    for t in threads:
        t.join(1)
    for path in [MILTER, LMTP]:
        path.unlink(missing_ok=True)
