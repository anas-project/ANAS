#!/usr/bin/env python3
"""Real Forgejo OIDC add/remove mapping probe with a test-only OIDC issuer.

Requires the disposable PostgreSQL upgrade fixture. This is protocol-level
integration evidence, not LLNG/Casdoor browser or directory acceptance.
"""
import base64
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.request
import urllib.error

ROOT=Path('/home/whl/anas-forgejo-1030/fixture')
BASE='http://127.0.0.1:13000'
DOCKER=['docker','-H','unix:///run/anas-fj-1030-docker.sock']


def probe_existing_session():
    """Distinguish an HTTP 200 error page from surviving authenticated access."""
    assert os.path.samestat(os.stat('/run/netns/anas-fj-1030'),os.stat('/proc/self/ns/net'))
    assert json.loads((ROOT/'oidc-result.json').read_text())['status']=='passed'
    password=(ROOT/'password').read_text()
    auth='Basic '+base64.b64encode(('anas-forgejo-upgrade-test:'+password).encode()).decode()
    def prohibit(value):
        request=urllib.request.Request(BASE+'/api/v1/admin/users/oidc-fixture',data=json.dumps({'prohibit_login':value}).encode(),method='PATCH',headers={'Authorization':auth,'Content-Type':'application/json'})
        with urllib.request.urlopen(request,timeout=30) as response:response.read()
    def read(opener,path,label):
        try:
            response=opener.open(BASE+path,timeout=30)
        except urllib.error.HTTPError as error:
            response=error
        with response:
            body=response.read()
            (ROOT/('session-'+label+'.html')).write_bytes(body)
            return {'status':response.status,'url':response.url,'sha256':hashlib.sha256(body).hexdigest(),'private_file_readable':body==b'OIDC revocation probe\n'}
    process=subprocess.Popen([str(ROOT.parent/'inputs'/'oidc-fixture'),'--claims',str(ROOT/'claims.json')],stdout=open(ROOT/'oidc-session-provider.log','wb'),stderr=subprocess.STDOUT)
    report={}
    try:
        for _ in range(50):
            try:urllib.request.urlopen('http://10.253.30.2:13001/.well-known/openid-configuration',timeout=2).close();break
            except Exception:time.sleep(.1)
        prohibit(False)
        browser=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        with browser.open(BASE+'/user/oauth2/anas',timeout=30) as response:
            assert '/user/login' not in response.url
            response.read()
        private='/oidc-fixture/identity-proof/raw/branch/probe-http/proof.txt'
        report['anonymous_private']=read(urllib.request.build_opener(),private,'anonymous-private')
        report['before_private']=read(browser,private,'before-private')
        report['before_settings']=read(browser,'/user/settings','before-settings')
        assert not report['anonymous_private']['private_file_readable'] and report['before_private']['private_file_readable']
        prohibit(True)
        report['after_private']=read(browser,private,'after-private')
        report['after_settings']=read(browser,'/user/settings','after-settings')
        report['status']='passed'
        print(json.dumps(report,indent=2),flush=True)
    finally:
        try:prohibit(False)
        finally:
            process.terminate();process.wait(timeout=10)
            (ROOT/'session-result.json').write_text(json.dumps(report,indent=2)+'\n')


def main():
    assert os.path.samestat(os.stat('/run/netns/anas-fj-1030'),os.stat('/proc/self/ns/net'))
    assert json.loads((ROOT/'result.json').read_text())['status']=='passed'
    password=(ROOT/'password').read_text()
    auth='Basic '+base64.b64encode(('anas-forgejo-upgrade-test:'+password).encode()).decode()
    checks=[]
    observations={}
    status='failed'
    def api(path,method='GET',body=None):
        req=urllib.request.Request(BASE+'/api/v1'+path,data=None if body is None else json.dumps(body).encode(),method=method,headers={'Authorization':auth,'Content-Type':'application/json'})
        with urllib.request.urlopen(req,timeout=30) as r:
            data=r.read();return json.loads(data) if data else None
    def record(name):
        checks.append(name);print('PASS',name,flush=True)
    claims={'sub':'stable-fixture-sub','preferred_username':'oidc-fixture','nickname':'oidc-fixture','name':'OIDC Fixture','email':'oidc-fixture@example.test','email_verified':True,'groups':['Developers','Admins']}
    (ROOT/'claims.json').write_text(json.dumps(claims))
    process=subprocess.Popen([str(ROOT.parent/'inputs'/'oidc-fixture'),'--claims',str(ROOT/'claims.json')],stdout=open(ROOT/'oidc-provider.log','wb'),stderr=subprocess.STDOUT)
    def configure(mapping):
        payload={'name':'anas','client_id':'fixture-client','client_secret':'fixture-secret','discovery_url':'http://10.253.30.2:13001/.well-known/openid-configuration','admin_group':'Admins','group_team_map':mapping}
        r=subprocess.run(DOCKER+['exec','-i','--user','1000:1000','fj-app','/usr/local/bin/anas-forgejo-entrypoint','oidc'],input=json.dumps(payload).encode(),capture_output=True,timeout=60)
        if r.returncode:raise RuntimeError('product OIDC helper failed')
    def login(label):
        browser=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        with browser.open(BASE+'/user/oauth2/anas',timeout=30) as response:
            (ROOT/('oidc-'+label+'.html')).write_bytes(response.read())
            assert '/user/login' not in response.url and '/user/sign_up' not in response.url, 'OIDC login did not complete'
        return api('/users/oidc-fixture'),browser
    try:
        for _ in range(50):
            try:urllib.request.urlopen('http://10.253.30.2:13001/.well-known/openid-configuration',timeout=2).close();break
            except Exception:time.sleep(.1)
        api('/orgs','POST',{'username':'upgrade-org','visibility':'private'})
        team=api('/orgs/upgrade-org/teams','POST',{'name':'reviewers','permission':'read','units':['repo.code']})
        manual=api('/orgs/upgrade-org/teams','POST',{'name':'manual','permission':'read','units':['repo.code']})
        mapping={'Developers':{'upgrade-org':['reviewers']}}
        configure(mapping);configure(mapping)
        user,browser=login('joined')
        original_id=user['id']
        assert user['is_admin'] is True
        assert any(x['login']=='oidc-fixture' for x in api('/teams/'+str(team['id'])+'/members'))
        api('/teams/'+str(manual['id'])+'/members/oidc-fixture','PUT')
        record('oidc-jit-group-add-and-admin-grant')
        claims['groups']=[];(ROOT/'claims.json').write_text(json.dumps(claims))
        user,browser=login('removed')
        assert user['is_admin'] is False
        assert not any(x['login']=='oidc-fixture' for x in api('/teams/'+str(team['id'])+'/members'))
        assert any(x['login']=='oidc-fixture' for x in api('/teams/'+str(manual['id'])+'/members'))
        record('oidc-group-removal-admin-downgrade-manual-team-preserved')
        claims['groups']=['Developers'];claims['preferred_username']='renamed-fixture';claims['nickname']='renamed-fixture';claims['email']='changed-fixture@example.test';(ROOT/'claims.json').write_text(json.dumps(claims))
        repo=api('/admin/users/oidc-fixture/repos','POST',{'name':'identity-proof','private':True,'auto_init':True})
        user,browser=login('renamed')
        assert user['id']==original_id and user['login']=='oidc-fixture' and user['email']=='oidc-fixture@example.test'
        assert api('/repos/oidc-fixture/identity-proof')['id']==repo['id']
        users=api('/admin/users')
        matched=[u for u in users if u.get('login_name')=='stable-fixture-sub']
        assert len(matched)==1 and matched[0]['id']==user['id']
        record('stable-sub-login-name-and-frozen-profile')
        configure({})
        record('empty-mapping-clears-auth-source')
        # Record actual CLI/API capabilities rather than infer an upgrade from
        # historical 15.0.7 behavior. Help output has no credentials.
        for kind in ['add-ldap','add-oauth','update-oauth']:
            r=subprocess.run(DOCKER+['exec','--user','1000:1000','fj-app','forgejo','admin','auth',kind,'--help'],capture_output=True,check=True,timeout=30)
            (ROOT/('cli-'+kind+'.txt')).write_bytes(r.stdout)
        assert b'--group-team-map' in (ROOT/'cli-add-oauth.txt').read_bytes()
        assert b'--group-team-map-removal' in (ROOT/'cli-update-oauth.txt').read_bytes()
        ldap=(ROOT/'cli-add-ldap.txt').read_text()
        assert '--synchronize-users' in ldap and '--group' not in ldap and '--uuid' not in ldap
        for path in ['/admin/auths','/admin/auth','/admin/auth/sources','/admin/users/oidc-fixture/sessions']:
            try:api(path)
            except urllib.error.HTTPError as e:assert e.code==404
            else:raise AssertionError('capability changed: '+path)
        record('17-cli-and-auth-session-api-probe')
        with urllib.request.urlopen(BASE+'/swagger.v1.json',timeout=30) as response:
            schema=json.load(response)
        (ROOT/'swagger.v1.json').write_text(json.dumps(schema))
        observations['admin_auth_session_logout_routes']=[p for p in schema['paths'] if any(s in p.lower() for s in ['auth','session','logout']) and p.startswith('/admin')]
        observations['admin_token_routes']=[p for p in schema['paths'] if p.startswith('/admin/users/') and '/tokens' in p]
        observations['edit_user_fields']=sorted(schema['definitions']['EditUserOption']['properties'])

        # FJPROBE-T-001/002: establish all four access paths before disabling
        # the isolated OIDC user, then record the actual revocation boundary.
        token=api('/admin/users/oidc-fixture/tokens','POST',{'name':'revocation-probe','scopes':['all']})
        subprocess.run(['ssh-keygen','-q','-t','ed25519','-N','','-f',str(ROOT/'oidc-key')],check=True)
        key=api('/admin/users/oidc-fixture/keys','POST',{'title':'revocation-probe','key':(ROOT/'oidc-key.pub').read_text()})
        (ROOT/'oidc-askpass').write_text('#!/bin/sh\ncase "$1" in *Username*) echo oidc-fixture;; *) printf "%s\\n" "$FJ_TEST_TOKEN";; esac\n')
        (ROOT/'oidc-askpass').chmod(0o700)
        env=dict(os.environ,GIT_ASKPASS=str(ROOT/'oidc-askpass'),GIT_TERMINAL_PROMPT='0',FJ_TEST_TOKEN=token['sha1'],GIT_SSH_COMMAND='ssh -i '+str(ROOT/'oidc-key')+' -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile='+str(ROOT/'known_hosts'))
        def git(*args):
            return subprocess.run(['git','-c','credential.helper=']+list(args),env=env,capture_output=True,timeout=45)
        remote=BASE+'/oidc-fixture/identity-proof.git'
        work=str(ROOT/'oidc-git')
        assert git('clone',remote,work).returncode==0
        (Path(work)/'proof.txt').write_text('OIDC revocation probe\n')
        assert git('-C',work,'add','proof.txt').returncode==0
        assert git('-C',work,'-c','user.name=Fixture','-c','user.email=fixture@example.test','commit','-m','probe').returncode==0
        ssh_remote='ssh://git@127.0.0.1:12222/oidc-fixture/identity-proof.git'
        def access():
            result={}
            try:
                with browser.open(BASE+'/user/settings',timeout=30) as response:
                    result['web_session']=response.status if '/user/login' not in response.url else 'login-redirect'
            except urllib.error.HTTPError as error:result['web_session']=error.code
            request=urllib.request.Request(BASE+'/api/v1/user',headers={'Authorization':'token '+token['sha1']})
            try:
                with urllib.request.urlopen(request,timeout=30) as response:result['api_token']=response.status
            except urllib.error.HTTPError as error:result['api_token']=error.code
            result['git_http_push_exit']=git('-C',work,'push',remote,'HEAD:refs/heads/probe-http').returncode
            result['git_ssh_push_exit']=git('-C',work,'push',ssh_remote,'HEAD:refs/heads/probe-ssh').returncode
            return result
        observations['before_prohibit_login']=access()
        assert observations['before_prohibit_login']=={'web_session':200,'api_token':200,'git_http_push_exit':0,'git_ssh_push_exit':0}
        full={'login_name':matched[0]['login_name'],'source_id':matched[0]['source_id']}
        try:
            api('/admin/users/oidc-fixture','PATCH',{'prohibit_login':True})
            observations['prohibit_login_required_fields']=['prohibit_login']
        except urllib.error.HTTPError as error:
            observations['minimal_prohibit_login_status']=error.code
            assert 400<=error.code<500
            api('/admin/users/oidc-fixture','PATCH',dict(full,prohibit_login=True))
            observations['prohibit_login_required_fields']=['prohibit_login','login_name','source_id']
        observations['after_prohibit_login']=access()
        api('/admin/users/oidc-fixture','PATCH',dict(full,prohibit_login=False))
        api('/admin/users/oidc-fixture/tokens/'+str(token['id']),'DELETE')
        api('/admin/users/oidc-fixture/keys/'+str(key['id']),'DELETE')
        record('prohibit-login-four-path-observations-and-admin-credential-revocation')
        status='passed'
    finally:
        (ROOT/'oidc-result.json').write_text(json.dumps({'status':status,'checks':checks,'observations':observations},indent=2)+'\n')
        process.terminate();process.wait(timeout=10)

if __name__=='__main__':
    if sys.argv[1:]==['--session-only']:probe_existing_session()
    else:
        assert len(sys.argv)==1
        main()
