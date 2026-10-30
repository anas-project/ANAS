#!/usr/bin/env python3
"""Exercise real IMAP/SMTP against the isolated mail fixture through SSH tunnels."""
# TEST_CASES: MAILPOC-T-001, MAILPOC-T-002, MAILPOC-T-003
import argparse
import base64
from email import policy
from email.message import EmailMessage
from email.parser import BytesParser
from email.utils import parseaddr
import hashlib
import imaplib
import json
import os
from pathlib import Path
import re
import smtplib
import socket
import ssl
import time

ROOT = Path(os.environ['MAIL_POC_EVIDENCE'])
if not ROOT.is_absolute() or 'mail-webmail-poc' not in str(ROOT):
    raise SystemExit('Explicit private mail PoC evidence directory required')
CA = ssl.create_default_context(cafile=str(ROOT / 'cert.pem'))
HOST = 'mail.mailtest.invalid'
DOMAIN = 'mailtest.invalid'
ADDRESS = os.environ.get('MAIL_POC_ADDRESS', 'alice@' + DOMAIN)
if not ADDRESS.endswith('@' + DOMAIN):
    raise SystemExit('Only the synthetic test domain is allowed')


def token(user='alice'):
    return json.loads((ROOT / f'tokens-{user}.json').read_text())['access_token']


def sasl(address, access_token):
    return f'user={address}\x01auth=Bearer {access_token}\x01\x01'.encode()


class IMAP(imaplib.IMAP4_SSL):
    def _create_socket(self, timeout):
        return CA.wrap_socket(socket.create_connection(('127.0.0.1', 19993), timeout), server_hostname=HOST)


def mailbox(address=ADDRESS, access_token=None):
    client = IMAP(HOST, 19993, ssl_context=CA, timeout=30)
    payload = sasl(address, token() if access_token is None else access_token)
    sent = False
    def response(_):
        nonlocal sent
        answer = b'' if sent else payload
        sent = True
        return answer
    try:
        client.authenticate('XOAUTH2', response)
    except Exception:
        client.shutdown()
        raise
    return client


def smtp(access_token=None, address=ADDRESS):
    client = smtplib.SMTP('127.0.0.1', 15887, timeout=30)
    client._host = HOST  # TLS SNI/certificate name, while TCP uses the SSH tunnel.
    client.ehlo()
    client.starttls(context=CA)
    client.ehlo()
    code, message = client.docmd('AUTH', 'XOAUTH2 ' + base64.b64encode(sasl(address, token() if access_token is None else access_token)).decode())
    if code == 334:
        code, message = client.docmd('')
    if code != 235:
        client.close()
        raise smtplib.SMTPAuthenticationError(code, b'Authentication rejected')
    return client


def message(kind, sender, recipient):
    msg = EmailMessage()
    msg['From'], msg['To'] = sender, recipient
    msg['Subject'] = 'ANAS mail PoC ' + kind
    msg['Message-ID'] = f'<mail-poc-{kind}@{DOMAIN}>'
    msg.set_content('Synthetic mail recovery and authorization fixture: ' + kind + '\n中文正文。\n')
    msg.add_attachment(b'ANAS PoC attachment\x00\x01\xff\n', maintype='application', subtype='octet-stream', filename='fixture.bin')
    return msg


def send(kind, envelope, visible, recipient='bob@'+DOMAIN):
    with smtp() as client:
        return client.send_message(message(kind, visible, recipient), from_addr=envelope, to_addrs=[recipient])


def snapshot(user='alice'):
    address = ADDRESS if user == 'alice' else user+'@'+DOMAIN
    output = {}
    with mailbox(address, token(user)) as client:
        status, folders = client.list()
        assert status == 'OK'
        for row in folders:
            # This fixture creates ASCII folders only. Reject unknown responses
            # instead of silently excluding a folder from recovery verification.
            match = re.match(rb'\(([^)]*)\) "[^"]*" (.+)$', row)
            assert match, row
            if b'\\Noselect' in match[1]:
                continue
            folder = match[2].decode().strip('"')
            status, _ = client.select('"'+folder+'"', readonly=True)
            assert status == 'OK', folder
            status, ids = client.uid('SEARCH', None, 'ALL')
            assert status == 'OK'
            messages = []
            for uid in ids[0].split():
                status, fetched = client.uid('FETCH', uid, '(BODY.PEEK[] FLAGS)')
                assert status == 'OK'
                part = next(item for item in fetched if isinstance(item, tuple))
                raw = part[1]
                msg = BytesParser(policy=policy.default).parsebytes(raw)
                if msg['Message-ID'] in ('<mail-poc-queued@'+DOMAIN+'>', '<mail-poc-queued-restore@'+DOMAIN+'>'):
                    continue  # Verified separately after releasing the restored queue.
                messages.append({'message_id':msg['Message-ID'], 'from':msg['From'], 'subject':msg['Subject'], 'return_path':msg['Return-Path'],
                                 'sha256':hashlib.sha256(raw).hexdigest(),
                                 'flags':sorted(flag.decode() for flag in imaplib.ParseFlags(part[0]) if flag != b'\\Recent'),
                                 'attachments':[hashlib.sha256(p.get_payload(decode=True)).hexdigest() for p in msg.iter_attachments()]})
            output[folder] = sorted(messages, key=lambda item: (item['message_id'] or '', item['sha256']))
    return output


def run(mode):
    checks = []
    def record(name, passed, **detail):
        checks.append({'name':name, 'passed':passed, **detail})
    if mode == 'auth':
        with mailbox() as client:
            record('valid_token_imap', client.select('INBOX')[0] == 'OK')
        with smtp():
            record('valid_token_smtp', True)
        for protocol, connect in [('imap', mailbox), ('smtp', smtp)]:
            for kind, address, access in [('invalid-token', ADDRESS, 'synthetic-invalid-token'), ('wrong-mailbox', 'bob@'+DOMAIN, token())]:
                try:
                    client = connect(address=address, access_token=access)
                    client.logout() if protocol == 'imap' else client.quit()
                    rejected = False
                except (imaplib.IMAP4.error, smtplib.SMTPAuthenticationError):
                    rejected = True
                record(protocol+'_'+kind+'_rejected', rejected)
    elif mode == 'seed':
        for alias in ('shopping', 'work'):
            send('incoming-'+alias, ADDRESS, ADDRESS, alias+'@'+DOMAIN)
        with mailbox() as client:
            if client.select('PoC')[0] != 'OK':
                assert client.create('PoC')[0] == 'OK'
            assert client.append('PoC', '(\\Seen \\Flagged)', None, message('folder', ADDRESS, ADDRESS).as_bytes())[0] == 'OK'
        deadline = time.monotonic()+45
        while time.monotonic() < deadline:
            state = snapshot()
            found = {item['message_id'] for items in state.values() for item in items}
            if all('<mail-poc-incoming-'+a+'@'+DOMAIN+'>' in found for a in ('shopping','work')):
                break
            time.sleep(1)
        record('two_aliases_deliver_to_one_mailbox', all('<mail-poc-incoming-'+a+'@'+DOMAIN+'>' in found for a in ('shopping','work')))
        record('folder_attachment_and_flags_seeded', any('\\Flagged' in x['flags'] and x['attachments'] for x in state.get('PoC',[])))
    elif mode in ('alias-negative', 'removed-alias'):
        forged = ('bob' if mode == 'alias-negative' else 'shopping')+'@'+DOMAIN
        for kind, envelope, visible in [('envelope', forged, forged), ('visible-from', ADDRESS, forged)]:
            try:
                send(mode+'-'+kind, envelope, visible)
                rejected = False
            except (smtplib.SMTPSenderRefused, smtplib.SMTPRecipientsRefused, smtplib.SMTPDataError):
                rejected = True
            record(mode+'_'+kind+'_rejected', rejected)
    elif mode == 'verify-ui':
        state=snapshot('bob')
        messages=[m for items in state.values() for m in items]
        for alias in ('shopping','work'):
            found=[m for m in messages if m['subject']=='ANAS UI alias '+alias]
            record('ui_alias_'+alias+'_received', len(found)==1 and parseaddr(found[0]['from'])[1]==alias+'@'+DOMAIN and parseaddr(found[0]['return_path'])[1]==alias+'@'+DOMAIN)
    elif mode == 'verify-history':
        expected=json.loads((ROOT/'mail-baseline.json').read_text())['alice']
        record('existing_mail_history_preserved', snapshot()==expected)
    elif mode in ('snapshot', 'verify'):
        state = {u:snapshot(u) for u in ('alice','bob')}
        baseline = ROOT/'mail-baseline.json'
        if mode == 'snapshot':
            baseline.write_text(json.dumps(state,ensure_ascii=False,indent=2)); baseline.chmod(0o600)
            record('nonempty_baseline', bool(state['alice'].get('PoC')))
        else:
            expected=json.loads(baseline.read_text())
            record('mail_content_attachments_folders_flags_match', state == expected)
            (ROOT/'mail-restored.json').write_text(json.dumps(state,ensure_ascii=False,indent=2))
    elif mode == 'queued':
        send('queued-restore', ADDRESS, ADDRESS, ADDRESS)
        record('queue_test_message_submitted', True)
    elif mode == 'verify-queued':
        with mailbox() as client:
            assert client.select('INBOX')[0] == 'OK'
            status, ids = client.uid('SEARCH', None, 'HEADER', 'Message-ID', '"<mail-poc-queued-restore@'+DOMAIN+'>"')
            record('restored_queue_delivered_once', status == 'OK' and len(ids[0].split()) == 1)
    else:
        raise ValueError(mode)
    return {'mode':mode, 'checks':checks, 'success':all(c['passed'] for c in checks)}


if __name__ == '__main__':
    parser=argparse.ArgumentParser()
    parser.add_argument('mode', choices=['auth','seed','alias-negative','removed-alias','verify-ui','verify-history','snapshot','verify','queued','verify-queued'])
    mode=parser.parse_args().mode
    try:
        result=run(mode)
    except Exception as error:
        # Library exceptions may contain protocol payloads. Keep only their type.
        result={'mode':mode,'success':False,'error_type':type(error).__name__}
    path=ROOT/(mode+'-protocol-result.json')
    path.write_text(json.dumps(result,indent=2)); path.chmod(0o600)
    print(json.dumps(result))
    raise SystemExit(0 if result['success'] else 1)
