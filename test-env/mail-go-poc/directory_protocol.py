#!/usr/bin/env python3
# TEST_CASES: MAILPOC-T-005
"""Real protocol/state oracles. Emits only named outcomes, never credentials."""
import base64
import hashlib
import http.client
import imaplib
import json
import os
from pathlib import Path
import smtplib
import socket
import ssl
import subprocess
import sys
import time
import traceback
import urllib.parse
from email.message import EmailMessage

ROOT=Path(os.environ['MAIL_GO_EVIDENCE']);assert ROOT.is_absolute() and 'mail-go-poc-directory' in str(ROOT)
CA=ssl.create_default_context(cafile=str(ROOT/'cert.pem'))
SECRETS=json.loads((ROOT/'secrets.json').read_text())
checks=[]
def check(name,result,**extra):
    checks.append({'name':name,'passed':bool(result),**extra});print(json.dumps(checks[-1]),flush=True);assert result,name
def wait(fn,timeout=60):
    until=time.monotonic()+timeout
    while time.monotonic()<until:
        if fn():return True
        time.sleep(.5)
    return False
class HTTP(http.client.HTTPSConnection):
    def connect(self):self.sock=CA.wrap_socket(socket.create_connection(('127.0.0.1',18884),10),server_hostname=self.host)
def webmail_status(user='alice'):
    session=json.loads((ROOT/f'session-{user}.json').read_text())
    c=HTTP('webmail.mailtest.invalid',18884,context=CA)
    c.request('GET','/?_task=mail&_action=refresh',headers={'Host':'webmail.mailtest.invalid:18884','Cookie':'; '.join(x['name']+'='+x['value'] for x in session['webmail_cookies'])})
    r=c.getresponse();r.read();status=r.status;c.close();return status
def api(path,form=None,user='alice',service=None):
    session=json.loads((ROOT/f'session-{user}.json').read_text())
    headers={'Host':'manage.mailtest.invalid:18884','Origin':'https://manage.mailtest.invalid:18884','Content-Type':'application/x-www-form-urlencoded','Cookie':'; '.join(c['name']+'='+c['value'] for c in session['cookies'])}
    if service:headers['Authorization']='Basic '+base64.b64encode((service+':'+SECRETS[service]).encode()).decode()
    if form is not None:form={**form,'csrf':session['csrf']}
    c=HTTP('manage.mailtest.invalid',18884,context=CA);c.request('GET' if form is None else 'POST',path,urllib.parse.urlencode(form) if form is not None else None,headers)
    r=c.getresponse();body=r.read();status=r.status;c.close()
    try:body=json.loads(body)
    except ValueError:body=body.decode()
    return status,body
def credential(box='box-01',user='alice'):
    code,c=api('/test/credential',{'box':box},user);assert code==200;return c
class IMAP(imaplib.IMAP4_SSL):
    def _create_socket(self,timeout):return CA.wrap_socket(socket.create_connection(('127.0.0.1',19994),timeout),server_hostname='mail.mailtest.invalid')
def imap(c,smtp_token=False,timeout=6):
    m=IMAP('mail.mailtest.invalid',19994,ssl_context=CA,timeout=timeout);sent=False
    def answer(_):
        nonlocal sent
        if sent:return b''
        sent=True
        return f"user={c['smtp_username'] if smtp_token else c['username']}\x01auth=Bearer {c['smtp_token'] if smtp_token else c['access_token']}\x01\x01".encode()
    try:m.authenticate('XOAUTH2',answer)
    except Exception:m.shutdown();raise
    return m
def smtp(c,imap_token=False):
    m=smtplib.SMTP('127.0.0.1',15888,timeout=20);m._host='mail.mailtest.invalid';m.ehlo();m.starttls(context=CA);m.ehlo()
    raw=f"user={c['username'] if imap_token else c['smtp_username']}\x01auth=Bearer {c['access_token'] if imap_token else c['smtp_token']}\x01\x01".encode()
    code,_=m.docmd('AUTH','XOAUTH2 '+base64.b64encode(raw).decode())
    if code==334:code,_=m.docmd('')
    if code!=235:m.close();raise smtplib.SMTPAuthenticationError(code,b'Rejected')
    return m
def send(m,label,address='alice@mailtest.invalid',visible=None):
    msg=EmailMessage();msg['From']=visible or address;msg['To']='bob@mailtest.invalid';msg['Subject']='Directory revoke '+label;msg.set_content('Synthetic directory/session verification')
    return m.send_message(msg,from_addr=address,to_addrs=['bob@mailtest.invalid'])
def denied_send(m,label):
    try:send(m,label);return False
    except smtplib.SMTPResponseException as e:return e.smtp_code//100==5
    except smtplib.SMTPServerDisconnected:return True
def imap_dead(m):
    try:return m.noop()[0]!='OK'
    except (imaplib.IMAP4.error,OSError,EOFError):return True
def new_denied(fn,c,**kw):
    if fn is imap:kw['timeout']=20
    try:m=fn(c,**kw);m.shutdown() if fn is imap else m.close();return False
    except (imaplib.IMAP4.error,smtplib.SMTPAuthenticationError):return True
def server(mode):
    out=subprocess.check_output(['ssh','-o','BatchMode=yes','whl@finance.hlong.wang','python3 /home/whl/anas-mail-go-test-20261024/source-directory/test-env/mail-go-poc/directory_server.py '+mode],stderr=subprocess.PIPE)
    return json.loads(out)
def book(c,**kw):return api('/internal/addressbooks/query',{'book':'company','session_ref':c['session_ref'],**kw},service='roundcube')

def directory_checks():
    c=credential();status,b=book(c);check('directory_users_and_groups',status==200 and len(b['contacts'])==2 and {x['name'] for x in b['groups']}=={'Finance','Nested','MailUsers'})
    check('directory_only_public_primary_addresses',sorted(e for p in b['contacts'] for e in p['email'])==['alice@mailtest.invalid','bob@mailtest.invalid'])
    before={p['email'][0]:p['ID'] for p in b['contacts']}
    finance=next(g['ID'] for g in b['groups'] if g['name']=='Finance')
    check('directory_group_membership_filters_contacts',[p['email'] for p in book(c,group=finance)[1]['contacts']]==[['alice@mailtest.invalid']])
    check('sources_only_company_readonly',api('/internal/addressbooks',{'session_ref':c['session_ref']},service='roundcube')[1]['sources']==[{'id':'company','name':'Company directory','readonly':True,'groups':True,'kind':'directory'}])
    check('shared_source_not_enabled',api('/internal/addressbooks/query',{'book':'shared','session_ref':c['session_ref']},service='roundcube')[0]==404)
    check('write_endpoint_not_enabled',api('/internal/addressbooks/write',{'book':'company','session_ref':c['session_ref']},service='roundcube')[0]==404)
    check('untrusted_backend_call_denied',api('/internal/addressbooks',{'session_ref':c['session_ref']})[0]==403)
    check('invalid_session_backend_call_denied',api('/internal/addressbooks',{'session_ref':'0'*64},service='roundcube')[0]==403)
    check('smtp_credential_cannot_login_imap',new_denied(imap,c,smtp_token=True))
    check('imap_credential_cannot_login_smtp',new_denied(smtp,c,imap_token=True))
    with smtp(c) as m:
        check('smtp_session_identity_accepts_legitimate_sender',send(m,'legitimate')=={})
        try:send(m,'forged','alice@mailtest.invalid','bob@mailtest.invalid');rejected=False
        except smtplib.SMTPResponseException as e:rejected=e.smtp_code//100==5
        check('smtp_session_identity_rejects_forged_from',rejected)
    before_cursor=json.loads(server('state')['directory'])['cursor'];started=time.monotonic();marker=server('profile')['marker']
    check('real_ad_profile_event_visible',wait(lambda:any(p['department']==marker and p['phone:work']=='+65 6000 0000' for p in book(c)[1]['contacts'])),elapsed=round(time.monotonic()-started,3))
    check('profile_consumed_new_journal_event',json.loads(server('state')['directory'])['cursor']>before_cursor)
    check('profile_keeps_stable_contact_ids',{p['email'][0]:p['ID'] for p in book(c)[1]['contacts']}==before)
    server('add-contact');check('new_ad_user_event_visible',wait(lambda:any(p['email']==['charlie@mailtest.invalid'] for p in book(c)[1]['contacts'])))
    server('remove-contact');check('deleted_ad_contact_disappears',wait(lambda:len(book(c)[1]['contacts'])==2))
    (ROOT/'profile-ready').write_text('ready')

def recovery_checks():
    c=credential();original=book(c)[1];ids=sorted(x['ID'] for x in original['contacts'])
    def visible(marker):
        try:return any(p['department']==marker for p in book(c)[1]['contacts'])
        except (OSError,KeyError,TypeError):return False
    def state():return json.loads(server('state')['directory'])
    before=state()['cursor']
    server('stop-go');marker=server('profile')['marker'];time.sleep(1);server('start-go')
    check('consumer_restart_reconciles_real_ad_change',wait(lambda:visible(marker)))
    check('consumer_restart_preserves_cursor',wait(lambda:state()['cursor']>before))
    before=state()['cursor'];server('stop-go')
    server('profile');marker=server('profile')['marker'];time.sleep(1)
    gap=server('journal-gap');check('real_journal_gap_present',gap['last_seq']>before+1)
    server('start-go');check('journal_gap_reconciles_latest_ad',wait(lambda:visible(marker)))
    check('journal_gap_explicitly_detected',wait(lambda:any('seq='+str(gap['last_seq'])+' gap=true' in line for line in server('reconcile-log')['lines'])))
    before=state();previous_logs=set(server('reconcile-log')['lines']);server('pause-go')
    try:
        marker=server('profile')['marker'];time.sleep(1);server('stop-directory')
    finally:server('unpause-go')
    try:
        check('ldap_failure_is_retried',wait(lambda:any('directory retry pending' in line and line not in previous_logs for line in server('reconcile-log')['lines']),20))
        during=state()
        check('ldap_failure_does_not_advance_cursor_or_delete_users',during['cursor']==before['cursor'] and during['users']==before['users'] and during['groups']==before['groups'])
    finally:server('start-directory')
    check('ldap_recovery_consumes_pending_event',wait(lambda:visible(marker)))
    check('recovery_keeps_contact_identity',sorted(x['ID'] for x in book(c)[1]['contacts'])==ids)

def revoke_checks(mode):
    a=credential();a2=credential(user='alice2');box2=credential('box-02');b=credential('box-03','bob')
    ia,ia2,ix,ib=imap(a),imap(a2),imap(box2),imap(b)
    sa,sa2,sb=smtp(a),smtp(a2),smtp(b)
    check(mode+'_connections_authenticated',ia.noop()[0]=='OK' and ib.noop()[0]=='OK')
    started=time.monotonic()
    if mode=='logout':check('go_logout_request',api('/logout',{})[0]==200)
    elif mode=='iam':
        (ROOT/'iam-ready').write_text('ready')
    elif mode=='nested':
        server('nested');time.sleep(3)
        check('nested_membership_still_admits',api('/')[0]==200 and ia.noop()[0]=='OK')
        server('remove-nested')
    elif mode=='direct':server('remove-direct')
    else:server(mode)
    check(mode+'_go_old_session_revoked',wait(lambda:api('/internal/session',{'session_ref':a['session_ref']},service='roundcube')[0]==403),elapsed=round(time.monotonic()-started,3))
    check(mode+'_old_roundcube_cookie_denied_before_deadline',webmail_status()==403 and time.monotonic()-started<60,elapsed=round(time.monotonic()-started,3))
    check(mode+'_old_imap_connection_kicked',wait(lambda:imap_dead(ia)))
    check(mode+'_second_mailbox_connection_kicked',wait(lambda:imap_dead(ix)))
    check(mode+'_old_smtp_cannot_send',denied_send(sa,mode+'-revoked'))
    check(mode+'_protocol_revocation_within_60s',time.monotonic()-started<60,elapsed=round(time.monotonic()-started,3))
    check(mode+'_old_credentials_cannot_reauthenticate',new_denied(imap,a) and new_denied(smtp,a))
    check(mode+'_bob_imap_survives',ib.noop()[0]=='OK')
    check(mode+'_bob_smtp_survives',send(sb,mode+'-bob','bob@mailtest.invalid')=={})
    if mode in ('logout','iam'):
        check(mode+'_other_alice_session_survives',ia2.noop()[0]=='OK' and api('/',user='alice2')[0]==200)
        check(mode+'_other_alice_smtp_survives',send(sa2,mode+'-alice2')=={})
    else:
        check(mode+'_all_alice_sessions_revoked',wait(lambda:imap_dead(ia2)) and api('/',user='alice2')[0]!=200)
        if mode!='delete':
            server('enable' if mode=='disable' else 'restore');time.sleep(3)
            check(mode+'_reenable_does_not_restore_old_session',api('/')[0]!=200 and new_denied(imap,a) and denied_send(sa,mode+'-reenabled'))
    for m in [ia,ia2,ix,ib]:
        try:m.shutdown()
        except Exception:pass
    for m in [sa,sa2,sb]:m.close()

mode=sys.argv[1]
try:
    if mode=='directory':directory_checks()
    elif mode=='recovery':recovery_checks()
    else:revoke_checks(mode)
except Exception as e:
    (ROOT/(mode+'-error.txt')).write_text(traceback.format_exc());checks.append({'name':mode+'_completed','passed':False})
finally:
    out=ROOT/(mode+'-protocol.json');out.write_text(json.dumps(checks,indent=2));out.chmod(0o600);print(json.dumps(checks))
if any(not c['passed'] for c in checks):sys.exit(1)
