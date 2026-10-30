#!/usr/bin/env python3
# TEST_CASES: MAILMOD-T-007
"""Inspect isolated Postfix/Rspamd transformations before native LMTP."""
import email, hashlib, json, os, pwd, re, secrets, smtplib, socket, struct, subprocess, threading, time
from pathlib import Path

header=Path('/etc/postfix/anas-intake-trusted-headers')
original=b'/^X-ANAS-Ingress-Key: [a-f0-9]{64}$/ WARN ANAS ingress trace\n'
config=Path('/etc/dovecot/conf.d/98-anas-lmtp-probe.conf')
script=Path('/var/mail-state/anas-lmtp-probe.sieve')
compiled=script.with_suffix('.svbin')
if header.read_bytes()!=original or any(p.exists() for p in [config,script,compiled]): raise SystemExit('isolated fixture conflict')
user='mbx_'+('a'*32)+'@mailboxes.anas.invalid'
ids={name:secrets.token_hex(32) for name in ['normal','discard','mime','folded']}
seen={}; expected={}; observations={}; failures=[]; stop=threading.Event(); ready=threading.Event()
socket_path=Path('/var/spool/postfix/private/anas-lmtp-probe.sock')
postfix=pwd.getpwnam('postfix')
if socket_path.exists() or socket_path.is_symlink(): raise SystemExit('test socket already exists')

def cmd(*args):
 p=subprocess.run(args,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=20)
 if p.returncode: raise RuntimeError('native command failed')
 return p.stdout.decode().strip()

def header_blocks(raw):
 header, separator, body=raw.partition(b'\r\n\r\n')
 if not separator: raise RuntimeError('missing CRLF header boundary')
 blocks=[]
 for line in header.split(b'\r\n'):
  if line.startswith((b' ',b'\t')):
   if not blocks: raise RuntimeError('orphan header continuation')
   blocks[-1]+=b'\r\n'+line
  else: blocks.append(line)
 return blocks,body

def listen():
 try:
  with socket.socket(socket.AF_UNIX,socket.SOCK_STREAM) as s:
   s.bind(str(socket_path));os.chown(socket_path,0,postfix.pw_gid);os.chmod(socket_path,0o660);s.listen(4);s.settimeout(1);ready.set()
   while not stop.is_set():
    try: conn,_=s.accept()
    except socket.timeout: continue
    with conn,conn.makefile('rwb',buffering=0) as f:
     pid,uid,gid=struct.unpack('3i',conn.getsockopt(socket.SOL_SOCKET,socket.SO_PEERCRED,12))
     if uid!=postfix.pw_uid or pid<1: raise RuntimeError('unexpected LMTP peer')
     conn.settimeout(30);f.write(b'220 isolated LMTP experiment\r\n');recipient=''
     while True:
      line=f.readline(4097)
      if not line or len(line)>4096: break
      command=line.upper()
      if command.startswith((b'LHLO ',b'EHLO ',b'HELO ')): f.write(b'250 isolated\r\n')
      elif command.startswith(b'MAIL FROM:'): f.write(b'250 sender ok\r\n')
      elif command.startswith(b'RCPT TO:'): recipient=line.decode().strip();f.write(b'250 recipient ok\r\n')
      elif command==b'DATA\r\n':
       f.write(b'354 continue\r\n');data=bytearray()
       while len(data)<262144:
        row=f.readline(8193)
        if not row or len(row)>8192: raise RuntimeError('bad message')
        if row==b'.\r\n': break
        if row.startswith(b'..'): row=row[1:]
        data.extend(row)
       msg=email.message_from_bytes(data); key=msg.get('X-ANAS-Ingress-Key','')
       if user not in recipient or key not in ids.values() or key in seen: raise RuntimeError('unexpected target or duplicate')
       source=expected[key]
       old,before=header_blocks(source); new,after=header_blocks(bytes(data))
       # Compare entire raw header blocks, including folding and duplicate
       # occurrences. Report generated header names only for synthetic inputs.
       # An ordered match alone is not a security proof or canonical binding.
       position=0; ordered=True; matched=set()
       for item in old:
        while position<len(new) and new[position]!=item: position+=1
        if position==len(new): ordered=False; break
        matched.add(position)
        position+=1
       added=[v.split(b':',1)[0].decode('ascii','replace').lower() for n,v in enumerate(new) if n not in matched] if ordered else []
       observations[key]={'original_headers_in_order':ordered,'body_identical':before==after,'original_header_count':len(old),'received_header_count':len(new),'added_header_names':added}
       with smtplib.LMTP('/var/run/dovecot/lmtp',timeout=20) as target:
        refused=target.sendmail('',[user],bytes(data))
        if refused: raise RuntimeError('LMTP rejected test delivery')
       seen[key]=msg.get('Subject','')
       f.write(b'250 test LMTP accepted\r\n')
      elif command.startswith(b'QUIT'): f.write(b'221 bye\r\n');break
      elif command.startswith(b'RSET'): f.write(b'250 reset\r\n')
      else: f.write(b'250 ok\r\n')
 except BaseException as e: failures.append(type(e).__name__);ready.set()

thread=threading.Thread(target=listen,daemon=True);thread.start()
if not ready.wait(4) or failures: raise SystemExit('test listener unavailable')
try:
 # A Unix socket limits access by account, not by executable. Test both an
 # unrelated account and a synthetic process using the Postfix account.
 connect_code="import socket,sys; s=socket.socket(socket.AF_UNIX); s.settimeout(4); s.connect(sys.argv[1]); f=s.makefile('rwb',buffering=0); f.readline(); f.write(b'QUIT\\r\\n'); f.readline(); f.close(); s.close()"
 def connect_as(uid,gid):
  return subprocess.run(['/usr/bin/python3','-c',connect_code,str(socket_path)],user=uid,group=gid,extra_groups=[],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=10)
 if connect_as(65534,65534).returncode==0: raise RuntimeError('unrelated UID accessed restricted socket')
 if connect_as(postfix.pw_uid,postfix.pw_gid).returncode!=0: raise RuntimeError('same-UID test process could not connect')
 if failures: raise RuntimeError('peer test broke listener')
 print('PASS: unrelated UID blocked; same Postfix UID alone cannot identify a process')
 if cmd('/usr/sbin/postconf','-h','virtual_transport')!='lmtp:unix:/var/run/dovecot/lmtp': raise RuntimeError('unexpected Dovecot LMTP route')
 script.write_text('if header :is "Subject" "isolated-lmtp-discard" { discard; stop; }\n');os.chmod(script,0o600);os.chown(script,5000,5000)
 config.write_text('sieve_script anas_lmtp_probe {\n type = before\n path = '+str(script)+'\n}\n');os.chmod(config,0o644)
 cmd('/usr/bin/sievec',str(script));os.chown(compiled,5000,5000);os.chmod(compiled,0o600)
 cmd('/usr/bin/doveconf','-n');cmd('/usr/sbin/dovecot','reload')
 header.write_bytes(b'/^X-ANAS-Ingress-Key: [a-f0-9]{64}$/ FILTER lmtp:unix:private/anas-lmtp-probe.sock\n')
 cmd('/usr/sbin/postfix','reload');time.sleep(.5)
 for name,key in ids.items():
  subject='isolated-lmtp-'+name
  body=('From: probe@example.test\r\nTo: '+user+'\r\nX-ANAS-Ingress-Key: '+key+'\r\nMessage-ID: <'+key+'@probe.invalid>\r\nSubject: '+subject+'\r\n\r\nSynthetic body.\r\n').encode()
  if name=='mime':
   body=('From: probe@example.test\r\nTo: '+user+'\r\nX-ANAS-Ingress-Key: '+key+'\r\nMessage-ID: <'+key+'@probe.invalid>\r\nSubject: '+subject+'\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary="anas-boundary"\r\n\r\n--anas-boundary\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n.dot-stuffed synthetic line\r\n--anas-boundary\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\nAAEC/f7/\r\n--anas-boundary--\r\n').encode()
  if name=='folded':
   body=('From: probe@example.test\r\nTo: '+user+'\r\nX-ANAS-Ingress-Key: '+key+'\r\nMessage-ID: <'+key+'@probe.invalid>\r\nSubject: '+subject+'\r\nX-Synthetic-Duplicate: first\r\n\tcontinued\r\nX-Synthetic-Duplicate: second\r\nX-Spam-Status: No, synthetic input\r\nDKIM-Signature: v=1; a=rsa-sha256; d=example.test; s=test; b=invalid\r\n\r\nSynthetic folded-header body.\r\n').encode()
  expected[key]=body
  with smtplib.SMTP('127.0.0.1',2525,timeout=30) as c:
   c.ehlo('localhost.localdomain');c.sendmail('',[user],body)
  deadline=time.monotonic()+50
  while key not in seen and not failures and time.monotonic()<deadline: time.sleep(.2)
  if failures or seen.get(key)!=subject: raise RuntimeError('LMTP test delivery missing')
  matching=cmd('/usr/bin/doveadm','search','-u',user,'HEADER','X-ANAS-Ingress-Key',key)
  if name!='discard' and not matching or name=='discard' and matching: raise RuntimeError('Sieve result did not match expected mailbox state')
  if name=='discard':
   logs=''
   for p in [Path('/var/log/mail/mail.log.1'),Path('/var/log/mail/mail.log')]:
    if p.is_file(): logs+='\n'+p.read_text(errors='replace')[-4*1024*1024:]
   if not any(key in line and 'discard' in line.lower() for line in logs.splitlines()): raise RuntimeError('native discard log absent')
  print('PASS: isolated LMTP '+name+' response and mailbox observation')
  print('OBSERVATION: '+name+' '+json.dumps(observations[key],sort_keys=True))
 # Determine whether a process with the Postfix UID can read an in-flight
 # token from the queue. The marker is synthetic and is never printed.
 # Delay the test transport before queuing exactly one isolated message.
 previous_defer=cmd('/usr/sbin/postconf','-h','defer_transports')
 if previous_defer: raise RuntimeError('unexpected deferred transport configuration')
 queue_id=''; label=secrets.token_hex(16)
 marker=hashlib.sha256(('11111111-1111-4111-8111-111111111111\0'+label+'\0'+'22222222-2222-4222-8222-222222222222').encode()).hexdigest()
 try:
  cmd('/usr/sbin/postconf','-e','defer_transports = lmtp')
  cmd('/usr/sbin/postfix','reload')
  body=('From: probe@example.test\r\nTo: '+user+'\r\nX-ANAS-Ingress-Key: '+marker+'\r\nSubject: isolated-queue-token\r\n\r\nSynthetic queued content.\r\n').encode()
  with smtplib.SMTP('127.0.0.1',2525,timeout=30) as c:
   c.ehlo('localhost.localdomain')
   if c.mail('')[0]!=250 or c.rcpt(user)[0]!=250: raise RuntimeError('queue probe envelope rejected')
   code,response=c.data(body)
  found=re.search(rb'queued as ([A-Za-z0-9]{5,64})',response,re.I)
  if code!=250 or not found: raise RuntimeError('queue probe was not accepted')
  queue_id=found.group(1).decode('ascii')
  root_view=subprocess.run(['/usr/sbin/postcat','-q',queue_id],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=15)
  same_uid=subprocess.run(['/usr/sbin/postcat','-q',queue_id],user=postfix.pw_uid,group=postfix.pw_gid,extra_groups=[],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=15)
  if root_view.returncode or marker.encode() not in root_view.stdout or marker in seen:
   raise RuntimeError('queue marker not provably retained before inspection')
  readable=same_uid.returncode==0 and marker.encode() in same_uid.stdout
  print('OBSERVATION: same_postfix_uid_reads_queue_marker '+json.dumps(readable))
  if readable:
   # Counterexample only: the temporary listener deliberately checks UID and
   # marker but not message content. A same-UID process reads the marker from
   # the queued original, then attempts a different synthetic body via LMTP.
   ids['queue-forgery']=marker;expected[marker]=body
   attack="""import re,smtplib,subprocess,sys
raw=subprocess.run(['/usr/sbin/postcat','-q',sys.argv[2]],stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True).stdout
m=re.search(rb'X-ANAS-Ingress-Key: ([a-f0-9]{64})',raw)
if not m: raise RuntimeError('queue marker unavailable')
message=b'From: probe@example.test\\r\\nTo: '+sys.argv[3].encode()+b'\\r\\nX-ANAS-Ingress-Key: '+m.group(1)+b'\\r\\nSubject: isolated-forged-queue-intent\\r\\n\\r\\nAltered synthetic content.\\r\\n'
with smtplib.LMTP(sys.argv[1],timeout=20) as client:
 if client.sendmail('',[sys.argv[3]],message): raise RuntimeError('synthetic forgery rejected')
"""
   attempt=subprocess.run(['/usr/bin/python3','-c',attack,str(socket_path),queue_id,user],user=postfix.pw_uid,group=postfix.pw_gid,extra_groups=[],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=30)
   if attempt.returncode or seen.get(marker)!='isolated-forged-queue-intent' or observations[marker]['body_identical'] or observations[marker]['original_headers_in_order']:
    raise RuntimeError('same-UID queue forgery outcome unconfirmed')
   if not cmd('/usr/bin/doveadm','search','-u',user,'HEADER','Subject','isolated-forged-queue-intent'):
    raise RuntimeError('synthetic forged body not observed in test mailbox')
   print('PASS: token-only test listener accepted a changed same-UID message; content binding is required')
   # Restore the actual intake header policy before invoking the real Go
   # observer. The synthetic queued original remains deferred; only the
   # altered message is present in the mailbox. The test's positive result is
   # an observed security counterexample, never a security acceptance.
   header.write_bytes(original);cmd('/usr/sbin/postfix','reload')
   try:
    env=dict(os.environ,MAIL_DMS_FORGERY_OBSERVE='1',MAIL_DMS_FORGERY_LABEL=label,MAIL_DMS_NATIVE_CONFIG='/input/native.json')
    observed=subprocess.run(['/input/dms.test','-test.v','-test.run','^TestNativeStoredTraceForgeryObservation$','-test.timeout','60s'],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=70)
    if observed.returncode or b'OBSERVATION: current observer accepted forged mailbox trace as stored' not in observed.stdout or b'--- PASS: TestNativeStoredTraceForgeryObservation ' not in observed.stdout:
     raise RuntimeError('native observer counterexample not observed')
    print('OBSERVATION: native observer accepted altered mailbox message as stored')
   finally:
    header.write_bytes(b'/^X-ANAS-Ingress-Key: [a-f0-9]{64}$/ FILTER lmtp:unix:private/anas-lmtp-probe.sock\n');cmd('/usr/sbin/postfix','reload')
 finally:
  if queue_id:
   cmd('/usr/sbin/postsuper','-d',queue_id)
  cmd('/usr/sbin/postconf','-e','defer_transports = '+previous_defer)
  cmd('/usr/sbin/postfix','reload')
 # An ordinary external SMTP sender cannot use the reserved header to select
 # the trusted intake transport. Confirm real mailbox delivery before deciding.
 spoof=secrets.token_hex(32)
 body=('From: attacker@example.test\r\nTo: alice@example.test\r\nX-ANAS-Ingress-Key: '+spoof+'\r\nSubject: isolated-external-spoof\r\n\r\nSynthetic body.\r\n').encode()
 with smtplib.SMTP('127.0.0.1',25,timeout=30) as c:
  c.ehlo('attacker.external.example.test');c.sendmail('', ['alice@example.test'], body)
 deadline=time.monotonic()+35
 while time.monotonic()<deadline:
  found=cmd('/usr/bin/doveadm','search','-u',user,'HEADER','Subject','isolated-external-spoof')
  if found: break
  time.sleep(.25)
 else: raise RuntimeError('external test message not observed in mailbox')
 if cmd('/usr/bin/doveadm','search','-u',user,'HEADER','X-ANAS-Ingress-Key',spoof) or len(seen)!=len(ids) or failures:
  raise RuntimeError('external reserved header selected trusted route')
 print('PASS: external forged trace did not select trusted listener')
 # Standard GTUBE reject pattern checks the active Rspamd path, without any
 # special test rule or production configuration change.
 spam=secrets.token_hex(32)
 body=('From: probe@example.test\r\nTo: '+user+'\r\nX-ANAS-Ingress-Key: '+spam+'\r\nMessage-ID: <'+spam+'@probe.invalid>\r\nSubject: isolated-gtube\r\n\r\nXJS*C4JDBQADN1.NSBN3*2IDNEN*GTUBE-STANDARD-ANTI-UBE-TEST-EMAIL*C.34X\r\n').encode()
 rejected=False
 try:
  with smtplib.SMTP('127.0.0.1',2525,timeout=30) as c:
   c.ehlo('localhost.localdomain');c.sendmail('',[user],body)
 except smtplib.SMTPDataError as e:
  rejected=500<=e.smtp_code<600
 if not rejected or len(seen)!=len(ids) or failures:
  raise RuntimeError('GTUBE not rejected before trusted listener')
 print('PASS: GTUBE rejected before trusted listener')
 print('PASS: Unix LMTP peer UID matched the isolated Postfix account')
 print('NOTE: no durable receipt, crash recovery or complete filtering-chain verification')
finally:
 header.write_bytes(original)
 p1=subprocess.run(['/usr/sbin/postfix','reload'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=20)
 for p in [config,script,compiled]: p.unlink(missing_ok=True)
 p2=subprocess.run(['/usr/sbin/dovecot','reload'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=20)
 stop.set();thread.join(timeout=5);socket_path.unlink(missing_ok=True)
 if p1.returncode or p2.returncode or thread.is_alive(): raise SystemExit('LMTP experiment cleanup failed')
