# TEST_CASES: MAILMOD-T-009
"""Verify isolated synthetic content without printing message data or addresses."""
import email, email.policy, os, pathlib, re, sys
if len(sys.argv) not in (3,4,5) or not re.fullmatch(r'[a-f0-9]{32}',sys.argv[2]):
    raise SystemExit('invalid verification input')
resend = len(sys.argv) == 5 and sys.argv[3] == 'resend' and re.fullmatch(r'cf-vps-test-[a-f0-9]{12}@mail-poc\.bengbu\.cc\.cd',sys.argv[4])
if len(sys.argv)==5 and not resend or len(sys.argv)==4 and sys.argv[3]!='whlsxl@126.com':
    raise SystemExit('invalid verification input')
p=pathlib.Path(sys.argv[1]);st=p.lstat()
if p.is_symlink() or not p.is_file() or st.st_uid!=os.getuid() or st.st_mode&0o077 or st.st_size>16*1024*1024: raise SystemExit('private bounded message required')
m=email.message_from_bytes(p.read_bytes(),policy=email.policy.default);nonce=sys.argv[2]
# The provider can replace the submitted Message-ID and visible To. In that
# mode bind identity to the per-test random subject, body, attachment and the
# independently checked source route; never call this a forged-To test.
ids=m.get_all('Message-ID',[]);subjects=m.get_all('Subject',[]);tos=m.get_all('To',[]);senders=m.get_all('From',[])
if len(ids)!=1 or not re.fullmatch(r'<[^<>\s@]+@[^<>\s@]+>',str(ids[0])): raise SystemExit('synthetic-message-id-invalid')
if not resend and ids[0]!='<anas-forward-'+nonce+'@mail-poc.hlong.wang>': raise SystemExit('synthetic-message-id-mismatch')
if len(subjects)!=1 or subjects[0]!='ANAS isolated forwarding '+nonce: raise SystemExit('synthetic-subject-mismatch')
if len(senders)!=1 or senders[0]!='probe@mail-poc.hlong.wang': raise SystemExit('synthetic-from-mismatch')
expected_to = {sys.argv[4], 'whlsxl@126.com'} if resend else {sys.argv[3] if len(sys.argv)==4 else 'misleading@foreign.example'}
if len(tos)!=1 or tos[0] not in expected_to: raise SystemExit('synthetic-visible-to-mismatch')
texts=[];attachments=[]
for part in m.walk():
    if part.get_content_type()=='text/plain' and part.get_content_disposition()!='attachment': texts.append(part.get_content())
    if part.get_filename()=='proof.bin': attachments.append(part.get_payload(decode=True))
if len(texts)!=1 or texts[0].strip()!='isolated-test-body '+nonce: raise SystemExit('synthetic-body-mismatch')
if attachments != [bytes([0,1,2,255])]: raise SystemExit('synthetic-attachment-mismatch')
if resend:
    print('synthetic headers, body and attachment matched; visible To ' + ('preserved' if tos[0]=='whlsxl@126.com' else 'rewritten'))
else:
    print('synthetic identity, body, attachment and visible To matched')
