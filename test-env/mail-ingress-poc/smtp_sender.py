#!/usr/bin/env python3
"""Send one synthetic message only to the explicitly authorized test domain."""
import email.policy
from email.message import EmailMessage
from email.utils import formatdate
import hashlib
import json
from pathlib import Path
import smtplib
import socket
import ssl
import sys
import time

ROOT=Path('/home/whl/anas-mail-ingress-test-20261025/evidence')
kind=sys.argv[1]
assert kind in ['a','b','fail','unknown']
path=ROOT/f'smtp-{kind}.json'
if path.exists():
    previous=json.loads(path.read_text())
    terminal=next((x['code'] for x in previous.get('responses',[]) if x['stage']=='data'),0)
    confirmed_rejection='--retry-rejected' in sys.argv[2:] and 400<=terminal<600
    assert not previous['accepted'] and (not previous['data_started'] or confirmed_rejection), 'Do not resend a possibly accepted SMTP transaction'
    path.rename(ROOT/f'smtp-{kind}-previous-{int(time.time())}.json')
sender='probe@sender.mail-poc.bengbu.cc.cd'
to=kind+'@mail-poc.bengbu.cc.cd'
msg=EmailMessage(policy=email.policy.SMTP)
msg['From']=sender
msg['To']='forged@example.org'
msg['Subject']='ANAS isolated SMTP '+kind
msg['Message-ID']='<same-smtp@sender.mail-poc.bengbu.cc.cd>'
msg['Date']=formatdate(localtime=False)
msg['X-Anas-Mail-Poc']=kind
msg.set_content('ANAS isolated SMTP '+kind+'\n中文附件验证\n')
msg.add_attachment(bytes([0,1,2,3,255]),maintype='application',subtype='octet-stream',filename='test.bin')
raw=msg.as_bytes()
eml=ROOT/f'smtp-{kind}.eml';eml.write_bytes(raw);eml.chmod(0o600)
result={'to':to,'from':sender,'sha256':hashlib.sha256(raw).hexdigest(),'started':time.time(),'connections':[],'responses':[],'accepted':False,'data_started':False}
client=None
try:
    for host in ['route2.mx.cloudflare.net','route1.mx.cloudflare.net']:
        for address in socket.getaddrinfo(host,25,socket.AF_INET,socket.SOCK_STREAM)[:2]:
            ip=address[4][0]
            class SMTP(smtplib.SMTP):
                def _get_socket(self,host,port,timeout):
                    return socket.create_connection((ip,port),timeout)
            try:
                phase='greeting'
                client=SMTP(host=host,port=25,timeout=12)
                phase='ehlo'
                code,text=client.ehlo('sender.mail-poc.bengbu.cc.cd');assert code==250
                phase='starttls'
                client.starttls(context=ssl.create_default_context())
                phase='tls-ehlo'
                code,text=client.ehlo('sender.mail-poc.bengbu.cc.cd');assert code==250
                result['connections'].append({'host':host,'ip':ip,'ready':True})
                break
            except Exception as e:
                if client:client.close()
                client=None
                result['connections'].append({'host':host,'ip':ip,'phase':locals().get('phase','greeting'),'error':type(e).__name__+': '+str(e)})
        if client:break
    assert client, 'No MX greeting available'
    client.sock.settimeout(30)
    for stage,fn in [('mail',lambda:client.mail(sender)),('rcpt',lambda:client.rcpt(to))]:
        code,text=fn();result['responses'].append({'stage':stage,'code':code,'text':text.decode(errors='replace')})
        if code>=400:raise smtplib.SMTPResponseException(code,text)
    # Persist the intent before DATA; an interrupted result must never trigger blind resend.
    result['data_started']=True;path.write_text(json.dumps(result,indent=2));path.chmod(0o600)
    code,text=client.data(raw)
    result['smtp_code']=code
    result['responses'].append({'stage':'data','code':code,'text':text.decode(errors='replace')})
    result['accepted']=code==250
    if not result['accepted']:result['error']='SMTP DATA rejected: '+str(code)
except Exception as e:
    result['error']=type(e).__name__+': '+str(e)
    if isinstance(e,smtplib.SMTPResponseException):result['smtp_code']=e.smtp_code
finally:
    if client:
        try:client.quit()
        except Exception:client.close()
    result['finished']=time.time();path.write_text(json.dumps(result,indent=2));path.chmod(0o600)
    print(json.dumps(result))
if not result['accepted']:sys.exit(1)
