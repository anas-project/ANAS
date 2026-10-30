#!/usr/bin/env python3
# TEST_CASES: MAILPOC-T-006
"""Real protocol oracle for alias UI sending and offline Go-stack recovery."""
import hashlib
import imaplib
import json
import re
import sys
import time
from email import policy
from email.message import EmailMessage
from email.parser import BytesParser
from email.utils import parseaddr
import protocol as p

MID='<mailgo-queued-recovery@mailtest.invalid>'
BOXES={'box-01':'alice','box-02':'alice','box-03':'bob'}
def message(kind,recipient):
    m=EmailMessage();m['From']='shopping@mailtest.invalid';m['To']=recipient
    m['Subject']='Recovery '+kind;m['Message-ID']='<mailgo-recovery-'+kind+'@mailtest.invalid>'
    m.set_content('Synthetic recovery content / 中文正文 / '+kind+'\n')
    m.add_attachment(b'ANAS recovery attachment\x00\xff\x01\n'+kind.encode(),maintype='application',subtype='octet-stream',filename=kind+'.bin')
    return m
def credentials(box):return p.credential(box,BOXES[box])
def snapshot():
    result={}
    for box in BOXES:
        output={}
        with p.imap(credentials(box)) as c:
            status,folders=c.list();assert status=='OK'
            for row in folders:
                match=re.match(rb'\(([^)]*)\) "[^"]*" (.+)$',row);assert match
                if b'\\Noselect' in match[1]:continue
                name=match[2].decode().strip('"');assert c.select('"'+name+'"',readonly=True)[0]=='OK'
                status,ids=c.uid('SEARCH',None,'ALL');assert status=='OK';messages=[]
                for uid in ids[0].split():
                    status,fetched=c.uid('FETCH',uid,'(BODY.PEEK[] FLAGS)');assert status=='OK'
                    part=next(x for x in fetched if isinstance(x,tuple));raw=part[1];msg=BytesParser(policy=policy.default).parsebytes(raw)
                    if msg['Message-ID']==MID:continue
                    messages.append({'id':str(msg['Message-ID'] or ''),'subject':str(msg['Subject'] or ''),'from':str(msg['From'] or ''),'return_path':str(msg['Return-Path'] or ''),
                        'sha256':hashlib.sha256(raw).hexdigest(),'flags':sorted(f.decode() for f in imaplib.ParseFlags(part[0]) if f!=b'\\Recent'),
                        'attachments':[hashlib.sha256(x.get_payload(decode=True)).hexdigest() for x in msg.iter_attachments()]})
                output[name]=sorted(messages,key=lambda x:(x['id'],x['sha256']))
        result[box]=output
    return result
def save(name,data):
    file=p.ROOT/name;file.write_text(json.dumps(data,ensure_ascii=False,indent=2));file.chmod(0o600)
mode=sys.argv[1]
try:
    if mode=='seed':
        for box in BOXES:
            with p.imap(credentials(box)) as c:
                for folder,flags in [('RecoveryArchive','(\\Seen \\Flagged)'),('Drafts','(\\Draft)')]:
                    existing=[x.decode() for x in c.list()[1]]
                    if not any(x.endswith(' '+folder) or x.endswith(' "'+folder+'"') for x in existing):assert c.create(folder)[0]=='OK'
                    assert c.append(folder,flags,None,message(box+'-'+folder,'alice@mailtest.invalid').as_bytes())[0]=='OK'
        with p.smtp(credentials('box-01')) as c:
            for alias in ('shopping','work'):
                msg=message('alias-'+alias,alias+'@mailtest.invalid')
                assert not c.send_message(msg,from_addr='shopping@mailtest.invalid',to_addrs=[alias+'@mailtest.invalid'])
        for _ in range(20):
            state=snapshot()
            if all(any(m['subject']=='Recovery alias-'+a for m in state['box-01']['INBOX']) for a in ('shopping','work')):break
            time.sleep(1)
        p.check('both_aliases_deliver_to_primary',all(any(m['subject']=='Recovery alias-'+a for m in state['box-01']['INBOX']) for a in ('shopping','work')))
        bob=state['box-03']['INBOX']
        for sender in ('shopping','work','projects'):
            rows=[m for m in bob if m['subject']=='Go UI '+sender]
            p.check('ui_sender_and_return_path_'+sender,len(rows)==1 and parseaddr(rows[0]['from'])[1]==sender+'@mailtest.invalid' and parseaddr(rows[0]['return_path'])[1]==sender+'@mailtest.invalid')
        p.check('independent_sent_separation',len(state['box-01']['Sent'])==2 and len(state['box-02']['Sent'])==1)
        p.check('second_box_does_not_receive_primary_aliases',not any(m['subject'].startswith('Recovery alias-') for m in state['box-02']['INBOX']))
        p.check('nonempty_attachment_flags_folder_fixture',all(state[b]['RecoveryArchive'][0]['attachments'] and state[b]['RecoveryArchive'][0]['flags']==['\\Flagged','\\Seen'] and state[b]['Drafts'][0]['flags']==['\\Draft'] for b in BOXES))
    elif mode=='snapshot':
        state=snapshot();save('mail-before.json',state)
        p.check('three_mailbox_baseline_saved',len(state)==3)
    elif mode=='verify':
        state=snapshot();save('mail-after.json',state);before=json.loads((p.ROOT/'mail-before.json').read_text())
        for box in BOXES:p.check(box+'_body_attachment_folders_flags_exact',state[box]==before[box])
        p.check('other_mailbox_token_denied_after_restore',p.rejected(p.imap,credentials('box-01'),credentials('box-02')['username']))
        status,result,_=p.api('/internal/addressbooks/query',{'book':'company','session_ref':credentials('box-01')['session_ref']},service='roundcube')
        p.check('company_directory_usable_after_restore',status==200 and len(result.get('contacts',[]))==2)
    elif mode=='queued':
        msg=message('held','work@mailtest.invalid');msg.replace_header('Message-ID',MID)
        save('queued-payload.json',{'body':msg.get_body(preferencelist=('plain',)).get_content(),'attachment':hashlib.sha256(next(msg.iter_attachments()).get_payload(decode=True)).hexdigest()})
        with p.smtp(credentials('box-01')) as c:p.check('queue_message_submitted',not c.send_message(msg,from_addr='shopping@mailtest.invalid',to_addrs=['work@mailtest.invalid']))
        with p.imap(credentials('box-01')) as c:
            c.select('INBOX');status,ids=c.uid('SEARCH',None,'HEADER','Message-ID','"'+MID+'"')
            p.check('held_message_not_delivered_before_backup',status=='OK' and not ids[0].split())
    elif mode=='verify-queued':
        before=json.loads((p.ROOT/'queued-payload.json').read_text())
        with p.imap(credentials('box-01')) as c:
            c.select('INBOX');status,ids=c.uid('SEARCH',None,'HEADER','Message-ID','"'+MID+'"')
            p.check('restored_queue_delivered_once',status=='OK' and len(ids[0].split())==1)
            status,rows=c.uid('FETCH',ids[0],'(BODY.PEEK[])');assert status=='OK'
            msg=BytesParser(policy=policy.default).parsebytes(next(x[1] for x in rows if isinstance(x,tuple)))
            p.check('queued_content_and_attachment_preserved',msg.get_body(preferencelist=('plain',)).get_content().replace('\r\n','\n')==before['body'].replace('\r\n','\n') and hashlib.sha256(next(msg.iter_attachments()).get_payload(decode=True)).hexdigest()==before['attachment'])
            p.check('queued_alias_sender_preserved',parseaddr(msg['From'])[1]=='shopping@mailtest.invalid' and parseaddr(msg['Return-Path'])[1]=='shopping@mailtest.invalid')
            time.sleep(3);status,ids=c.uid('SEARCH',None,'HEADER','Message-ID','"'+MID+'"');p.check('no_duplicate_delivery',status=='OK' and len(ids[0].split())==1)
    else:raise ValueError('Unknown mode')
except Exception as e:
    save('recovery-'+mode+'-error.json',{'type':type(e).__name__,'message':str(e)})
    if not p.checks or p.checks[-1]['passed']:p.checks.append({'name':'completed','passed':False})
finally:
    save('recovery-'+mode+'-result.json',p.checks);print(json.dumps(p.checks))
if any(not x['passed'] for x in p.checks):sys.exit(1)
