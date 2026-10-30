#!/usr/bin/env python3
"""Synthetic SMTP source: only the approved osa host and fixture recipients."""
import json
import re
import smtplib
import sys
import time
from pathlib import Path

label,recipient=sys.argv[1:3]
assert re.fullmatch(r'[a-z0-9-]{1,32}',label)
assert recipient in ['a','b','unknown','relay']
target={'a':'box-a@relay.ingress.invalid','b':'box-b@relay.ingress.invalid','unknown':'absent@relay.ingress.invalid','relay':'outside@example.net'}[recipient]
raw=(f'From: sender@example.net\r\nTo: forged@example.org\r\nMessage-ID: <same@sender.example>\r\nSubject: {label}\r\n'
     'MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=poc\r\n\r\n'
     '--poc\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n云中转验证\r\n'
     '--poc\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\nAAECA/8=\r\n--poc--\r\n')
r={'label':label,'recipient':target,'raw':raw,'started':time.time(),'data_started':False}
directory=Path('/home/whl/anas-mail-ingress-test-20261025/evidence-scheduler')
path=directory/('relay-smtp-'+label+'.json')
if path.exists():
    previous=json.loads(path.read_text())
    assert not previous['data_started'],'Do not resend after an unknown or accepted DATA transaction'
    path.rename(path.with_name(path.stem+'-before-data-'+str(time.time_ns())+'.json'))
def save():path.write_text(json.dumps(r,ensure_ascii=False,indent=2));path.chmod(0o600)
save()
try:
    with smtplib.SMTP('168.138.50.59',25,timeout=15) as s:
        s.ehlo('mail.ingress.invalid');r['mail_code']=s.mail('sender@example.net')[0]
        r['rcpt_code']=s.rcpt(target)[0];save()
        if r['rcpt_code']==250:
            r['data_started']=True;save()
            code,message=s.data(raw.encode());r['data_code']=code;r['reply']=message.decode(errors='replace')
except Exception as e:r['error']=type(e).__name__+': '+str(e);raise
finally:
    r['finished']=time.time();save();print(json.dumps(r,ensure_ascii=False))
