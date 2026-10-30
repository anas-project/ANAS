#!/usr/bin/env python3
# TEST_CASES: MAILPOC-T-004, MAILPOC-T-006
"""Independent real SMTP/IMAP oracles for the Go/RC fixture."""
import base64
from email.message import EmailMessage
from email.parser import BytesParser
from email import policy
import http.client
import hashlib
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
import urllib.parse

ROOT=Path(os.environ['MAIL_GO_EVIDENCE']);assert ROOT.is_absolute() and 'mail-go-poc' in str(ROOT)
CA=ssl.create_default_context(cafile=str(ROOT/'cert.pem'))
SECRETS=json.loads((ROOT/'secrets.json').read_text())
ORIGIN='https://manage.mailtest.invalid:18884'
checks=[]
def check(name,result,**details):
    checks.append({'name':name,'passed':bool(result),**details})
    assert result,name
class HTTP(http.client.HTTPSConnection):
    def connect(self):self.sock=CA.wrap_socket(socket.create_connection(('127.0.0.1',18884),10),server_hostname='manage.mailtest.invalid')
def api(path,form=None,user='alice',service=None):
    session=json.loads((ROOT/f'session-{user}.json').read_text())
    headers={'Host':'manage.mailtest.invalid:18884','Origin':ORIGIN,'Content-Type':'application/x-www-form-urlencoded','Cookie':'; '.join(c['name']+'='+c['value'] for c in session['cookies'])}
    if service:headers['Authorization']='Basic '+base64.b64encode((service+':'+SECRETS[service]).encode()).decode()
    method='GET' if form is None else 'POST'
    if form is not None:form={**form,'csrf':session['csrf']}
    c=HTTP('manage.mailtest.invalid',18884,context=CA);c.request(method,path,urllib.parse.urlencode(form) if form is not None else None,headers)
    r=c.getresponse();body=r.read();status=r.status;location=r.getheader('Location');c.close()
    try:body=json.loads(body)
    except ValueError:body=body.decode()
    return status,body,location
def credential(box='box-01',user='alice'):
    status,c,_=api('/test/credential',{'box':box},user);assert status==200;return c
def sasl(c,login=None,protocol='imap'):
    username=c['smtp_username'] if protocol=='smtp' else c['username']
    token=c['smtp_token'] if protocol=='smtp' else c['access_token']
    return f"user={login or username}\x01auth=Bearer {token}\x01\x01".encode()
class IMAP(imaplib.IMAP4_SSL):
    def _create_socket(self,timeout):return CA.wrap_socket(socket.create_connection(('127.0.0.1',19994),timeout),server_hostname='mail.mailtest.invalid')
def imap(c,login=None):
    m=IMAP('mail.mailtest.invalid',19994,ssl_context=CA,timeout=20);sent=False
    def answer(_):
        nonlocal sent
        if sent:return b''
        sent=True;return sasl(c,login)
    try:m.authenticate('XOAUTH2',answer)
    except Exception:m.shutdown();raise
    return m
def smtp(c,login=None):
    m=smtplib.SMTP('127.0.0.1',15888,timeout=20);m._host='mail.mailtest.invalid';m.ehlo();m.starttls(context=CA);m.ehlo()
    code,_=m.docmd('AUTH','XOAUTH2 '+base64.b64encode(sasl(c,login,'smtp')).decode())
    if code==334:code,_=m.docmd('')
    if code!=235:m.close();raise smtplib.SMTPAuthenticationError(code,b'Rejected')
    return m
def send(m,name,envelope,visible,recipient='bob@mailtest.invalid'):
    msg=EmailMessage();msg['From']=visible;msg['To']=recipient;msg['Subject']='Go protocol '+name;msg['Message-ID']='<mailgo-'+name+'@mailtest.invalid>';msg.set_content('Synthetic mail '+name)
    return m.send_message(msg,from_addr=envelope,to_addrs=[recipient])
def snapshot(c,folder='INBOX'):
    with imap(c) as m:
        ok,_=m.select(folder,readonly=True);assert ok=='OK'
        _,ids=m.search(None,'ALL');out=[]
        for n in ids[0].split():
            _,data=m.fetch(n,'(BODY.PEEK[])');raw=next(p[1] for p in data if isinstance(p,tuple));msg=BytesParser(policy=policy.default).parsebytes(raw)
            out.append({**{h:str(msg[h] or '') for h in ('Message-ID','Subject','From','Return-Path')},'sha256':hashlib.sha256(raw).hexdigest()})
        return out
def server(mode):
    result=subprocess.check_output(['ssh','-o','BatchMode=yes','whl@finance.hlong.wang','python3 /home/whl/anas-mail-go-test-20261024/source/mail-go-poc/server.py '+mode],stderr=subprocess.PIPE)
    return json.loads(result)
def rejected(conn,c,login=None):
    try:
        m=conn(c,login);m.logout() if conn==imap else m.quit();return False
    except (imaplib.IMAP4.error,smtplib.SMTPAuthenticationError):return True
def denied_send(m,name,envelope,visible,code_range=5):
    try:send(m,name,envelope,visible);return False
    except smtplib.SMTPResponseException as e:return e.smtp_code//100==code_range
def run():
    assert api('/alias',{'box':'box-01','address':'webui@mailtest.invalid','op':'add'})[0]==303
    # Native DMS asynchronously reloads maps/auth after a file change. This is
    # fixture settling time, not proof of a production provisioning ready state.
    time.sleep(12)
    a=credential();b=credential('box-02');bob=credential('box-03','bob')
    for name,c in [('primary',a),('independent',b),('bob',bob)]:
        with imap(c) as m:check(name+'_imap',m.select('INBOX')[0]=='OK')
        with smtp(c):check(name+'_smtp',True)
    for conn,name in [(imap,'imap'),(smtp,'smtp')]:
        check(name+'_same_owner_other_mailbox_denied',rejected(conn,a,b['username']))
        check(name+'_other_owner_denied',rejected(conn,a,bob['username']))
        check(name+'_invalid_token_denied',rejected(conn,{**a,'access_token':'invalid-token','smtp_token':'invalid-token'}))
    check('go_denies_other_owner_mailbox',api('/test/credential',{'box':'box-03'})[0]==403)
    state='s'*64
    status,_,location=api('/handoff?box=box-01&state='+state);check('handoff_created',status==303)
    code=urllib.parse.parse_qs(urllib.parse.urlsplit(location).query)['mailgo_code'][0]
    check('handoff_wrong_state_denied',api('/internal/redeem',{'code':code,'state':'wrong'},service='roundcube')[0]==403)
    check('handoff_redeemed',api('/internal/redeem',{'code':code,'state':state},service='roundcube')[0]==200)
    check('handoff_replay_denied',api('/internal/redeem',{'code':code,'state':state},service='roundcube')[0]==403)
    check('introspection_without_service_auth_denied',api('/internal/introspect',{'token':a['access_token']})[0]==403)
    print('Authentication and handoff checks passed',flush=True)
    a=credential();b=credential('box-02')
    with smtp(a) as m:
        for alias in ('shopping','work','webui'):send(m,'delivery-'+alias,'alice@mailtest.invalid','alice@mailtest.invalid',alias+'@mailtest.invalid')
        send(m,'separate-delivery','alice@mailtest.invalid','alice@mailtest.invalid','projects@mailtest.invalid')
        check('visible_other_owner_denied',denied_send(m,'bad-from-bob','alice@mailtest.invalid','bob@mailtest.invalid'))
        check('visible_same_owner_other_box_denied',denied_send(m,'bad-from-projects','alice@mailtest.invalid','projects@mailtest.invalid'))
        check('envelope_other_owner_denied',denied_send(m,'bad-envelope','bob@mailtest.invalid','alice@mailtest.invalid'))
        check('technical_sender_denied',denied_send(m,'bad-technical',a['username'],a['username']))
    time.sleep(3)
    primary=snapshot(credential());second=snapshot(credential('box-02'));recipient=snapshot(credential('box-03','bob'))
    check('three_aliases_deliver_to_primary',all(any(x['Subject']=='Go protocol delivery-'+s for x in primary) for s in ('shopping','work','webui')))
    check('independent_delivery_separate',any(x['Subject']=='Go protocol separate-delivery' for x in second) and not any(x['Subject']=='Go protocol separate-delivery' for x in primary))
    for sender in ('shopping','work','projects'):
        rows=[x for x in recipient if x['Subject']=='Go UI '+sender]
        check('independent_imap_ui_message_'+sender,len(rows)>=1 and all(sender+'@mailtest.invalid' in x['From'] and sender+'@mailtest.invalid' in x['Return-Path'] for x in rows))
    check('separate_sent_folders',len(snapshot(credential(),'Sent'))>=2 and len(snapshot(credential('box-02'),'Sent'))>=1)
    check('alias_persisted_in_ad','smtp:webui@mailtest.invalid' in server('ad')['proxyAddresses'])
    check('remove_alias_via_go',api('/alias',{'box':'box-01','address':'webui@mailtest.invalid','op':'remove'})[0]==303)
    check('alias_removed_from_ad','smtp:webui@mailtest.invalid' not in server('ad')['proxyAddresses'])
    incoming_removed=False
    for _ in range(20):
        with smtplib.SMTP('127.0.0.1',15254,timeout=10) as inbound:
            inbound._host='mail.mailtest.invalid';inbound.ehlo();inbound.starttls(context=CA);inbound.ehlo()
            # Null reverse-path avoids unrelated DNS sender-domain checks in
            # this deliberately offline .invalid fixture. Also test a known
            # valid recipient so a blanket SMTP rejection cannot pass.
            inbound.mail('');status,_=inbound.rcpt('webui@mailtest.invalid')
            valid_status,_=inbound.rcpt('shopping@mailtest.invalid')
            incoming_removed=status//100==5 and valid_status//100==2
        if incoming_removed:break
        time.sleep(2)
    check('removed_alias_incoming_recipient_denied',incoming_removed)
    with smtp(credential()) as m:
        check('removed_alias_visible_from_denied',denied_send(m,'removed-from','alice@mailtest.invalid','webui@mailtest.invalid'))
        check('removed_alias_envelope_denied',denied_send(m,'removed-envelope','webui@mailtest.invalid','alice@mailtest.invalid'))
    check('old_alias_mail_retained',any(x['Subject']=='Go protocol delivery-webui' for x in snapshot(credential())))
    print('Delivery, sender authorization and AD alias checks passed',flush=True)
    lifecycle()

def lifecycle():
    expiring=credential()
    before=credential();live=imap(before);live.select('INBOX')
    server('disable')
    try:
        check('disabled_box_new_imap_denied',rejected(imap,before))
        check('disabled_box_new_smtp_denied',rejected(smtp,before))
        # Record the real limit: token revocation cannot kill an already-authenticated IMAP connection.
        checks.append({'name':'existing_imap_after_disable','observation':live.noop()[0],'passed':True,'limitation':'separate connection termination required'})
    finally:live.logout();server('enable')
    c=credential();m=smtp(c)
    server('stop-go')
    try:
        check('go_outage_existing_smtp_tempfails',denied_send(m,'outage','alice@mailtest.invalid','alice@mailtest.invalid',4))
        outcome='server rejected authentication'
        try:blocked=rejected(imap,c)
        except TimeoutError:blocked=True;outcome='client timed out after 20 seconds without authentication'
        check('go_outage_new_auth_not_granted',blocked,outcome=outcome)
    finally:m.close();server('start-go')
    for _ in range(45):
        try:
            if api('/health')[0]==200:break
        except (OSError,http.client.HTTPException):pass
        time.sleep(2)
    # Real refresh rotation and session revocation, not mock tokens.
    remaining=expiring['expires_at']-time.time()+2
    if remaining>0:time.sleep(remaining)
    check('expired_access_imap_denied',rejected(imap,expiring))
    check('expired_access_smtp_denied',rejected(smtp,expiring))
    c=credential();status,new,_=api('/internal/refresh',{'refresh_token':c['refresh_token']},service='roundcube')
    check('refresh_rotates',status==200 and new['access_token']!=c['access_token'])
    check('refresh_replay_denied',api('/internal/refresh',{'refresh_token':c['refresh_token']},service='roundcube')[0]==403)
    held=smtp(new)
    try:
        check('go_logout',api('/logout',{})[0]==200)
        check('logout_revokes_new_imap',rejected(imap,new))
        check('logout_revokes_new_smtp',rejected(smtp,new))
        try:
            send(held,'existing-session-after-logout','alice@mailtest.invalid','alice@mailtest.invalid')
            observation='accepted'
        except smtplib.SMTPResponseException:observation='rejected'
        check('existing_smtp_after_logout_rejected',observation=='rejected')
    finally:held.close()
def continuity(mode):
    state={}
    for box in ('box-01','box-02'):
        state[box]={folder:snapshot(credential(box),folder) for folder in ('INBOX','Sent','Drafts')}
    p=ROOT/'continuity-mail-before.json'
    if mode=='continuity-before':
        p.write_text(json.dumps(state,indent=2));p.chmod(0o600);check('mail_continuity_baseline_saved',True)
    else:
        check('all_original_mail_hashes_and_folders_preserved',state==json.loads(p.read_text()))
        before=json.loads((ROOT/'rc-state-before.json').read_text());after=json.loads((ROOT/'rc-state-after.json').read_text())
        check('all_roundcube_users_and_preferences_preserved',before['users']==after['users'])
        uid=next(x['user_id'] for x in after['users'] if x['username']=='box-01@boxes.mailtest.invalid')
        check('new_public_address_default_identity',any(x['user_id']==uid and x['email']=='alice.new@mailtest.invalid' and x['standard']==1 for x in after['identities']))
        check('old_address_remains_same_mailbox_alias',any(x['user_id']==uid and x['email']=='alice@mailtest.invalid' and x['standard']==0 for x in after['identities']))

if __name__=='__main__':
    mode=sys.argv[1] if len(sys.argv)>1 else 'protocol'
    try:
        if mode.startswith('continuity-'):continuity(mode)
        elif mode=='lifecycle':lifecycle()
        else:run()
    except Exception as e:
        (ROOT/(mode+'-error.txt')).write_text(type(e).__name__+': '+str(e));(ROOT/(mode+'-error.txt')).chmod(0o600)
        if not checks or checks[-1].get('passed') is not False:checks.append({'name':'protocol_completed','passed':False})
    finally:
        p=ROOT/(mode+'-result.json');p.write_text(json.dumps(checks,indent=2));p.chmod(0o600);print(json.dumps(checks))
    if any(not x['passed'] for x in checks):sys.exit(1)
