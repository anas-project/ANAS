#!/usr/bin/env python3
"""Real AD/IAM/browser checks for the explicitly owned Forgejo acceptance stack.

Run after an ordinary successful ANAS apply, inside the dedicated namespace.
Uses existing PyYAML and the repository's Playwright version in a test image.
All credentials, screenshots and command details remain in the private root.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import ssl
import subprocess
import time
import urllib.error
import urllib.request
import urllib.parse

import yaml

ROOT = Path('/home/whl/anas-fj-iam-20261030')
SOCKET = os.environ.get('DOCKER_HOST', 'unix:///run/anas-fj-iam-1030-docker.sock')
BASE = 'https://git.fj-iam.test:19000'
BROWSER_IMAGE = 'mcr.microsoft.com/playwright:v1.62.1-noble'

DIRECTORY = r'''
import json,os,sys,ldb
from samba.auth import system_session
from samba.credentials import Credentials,DONT_USE_KERBEROS
from samba.param import LoadParm
from samba.samdb import SamDB
p=json.load(sys.stdin)
lp=LoadParm();lp.load('/etc/samba/smb.conf')
credentials=Credentials();credentials.guess(lp)
credentials.set_username(os.environ['SAMBA_DC_ADMIN_NAME'])
credentials.set_password(os.environ['SAMBA_DC_ADMIN_PASSWORD'])
credentials.set_kerberos_state(DONT_USE_KERBEROS)
db=SamDB(url='ldap://127.0.0.1',credentials=credentials,lp=lp)
domain=str(db.domain_dn())
assert domain.lower()=='dc=fj-iam,dc=test'
marker='ANAS Forgejo IAM acceptance 20261030'
def relative(key):
 value=os.environ[key]
 assert value.lower().endswith(','+domain.lower())
 return value[:-(len(domain)+1)]
def user(name):
 assert name in p['users'].values()
 rows=db.search(base=domain,expression='(sAMAccountName='+name+')',attrs=['description','anasIdentityAnchor','memberOf'])
 assert len(rows)==1 and str(rows[0].get('description',[''])[0])==marker
 return rows[0]
if p['action']=='create':
 rows=db.search(base=domain,expression='(sAMAccountName=forgejo-reviewers)',attrs=['description'])
 if not rows:
  db.newgroup('forgejo-reviewers',groupou=relative('SAMBA_DC_BASE_GROUPS_DN'))
  rows=db.search(base=domain,expression='(sAMAccountName=forgejo-reviewers)',attrs=['description'])
  change=ldb.Message();change.dn=rows[0].dn
  change['description']=ldb.MessageElement(marker,ldb.FLAG_MOD_REPLACE,'description');db.modify(change)
 else:assert str(rows[0].get('description',[''])[0])==marker
 for role,name in p['users'].items():
  rows=db.search(base=domain,expression='(sAMAccountName='+name+')',attrs=['description'])
  if not rows:
   db.newuser(name,p['password'],userou=relative('SAMBA_DC_BASE_USERS_DN'))
   row=db.search(base=domain,expression='(sAMAccountName='+name+')',attrs=[])[0]
   change=ldb.Message();change.dn=row.dn
   for key,value in {'description':marker,'displayName':name,'mail':name+'@fj-iam.test'}.items():
    change[key]=ldb.MessageElement(value,ldb.FLAG_MOD_REPLACE,key)
   db.modify(change)
  else:assert str(rows[0].get('description',[''])[0])==marker
  groups=['forgejo-reviewers'] if role=='deny' else ['APP_forgejo']
  if role=='user':groups.append('forgejo-reviewers')
  if role=='admin':groups.append('Admins')
  member_dn=str(user(name).dn).casefold()
  for group in groups:
   group_rows=db.search(base=domain,expression='(&(objectClass=group)(sAMAccountName='+group+'))',attrs=['member'])
   assert len(group_rows)==1
   if member_dn not in {str(value).casefold() for value in group_rows[0].get('member',[])}:
    db.add_remove_group_members(group,[name],add_members_operation=True)
elif p['action']=='drop-admin':
 user(p['users']['admin']);db.add_remove_group_members('Admins',[p['users']['admin']],add_members_operation=False)
elif p['action']=='drop-reviewer':
 user(p['users']['user']);db.add_remove_group_members('forgejo-reviewers',[p['users']['user']],add_members_operation=False)
else:assert p['action']=='inspect'
print(json.dumps({role:{'anchor':str(user(name).get('anasIdentityAnchor',[''])[0]),'groups':[str(v) for v in user(name).get('memberOf',[])]} for role,name in p['users'].items()}))
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--provider', choices=['casdoor', 'llng'], required=True)
    parser.add_argument('--browser-ws-file', help='private JSON file for an existing SSH loopback browser server')
    parser.add_argument('--hosting-only', action='store_true', help='run hosting smoke separately from IAM acceptance')
    args = parser.parse_args()
    args.browser_ws_endpoint = None
    if args.browser_ws_file:
        endpoint_file = Path(args.browser_ws_file)
        assert endpoint_file == ROOT / 'inputs/browser-server.json'
        assert endpoint_file.stat().st_mode & 0o077 == 0
        args.browser_ws_endpoint = json.loads(endpoint_file.read_text())['remote_endpoint']
    if args.browser_ws_endpoint:
        parsed = urllib.parse.urlsplit(args.browser_ws_endpoint)
        assert parsed.scheme == 'ws' and parsed.netloc == '127.0.0.1:19999'
        assert parsed.path.startswith('/') and not parsed.query and not parsed.fragment
    assert os.path.samestat(os.stat('/run/netns/anas-fj-iam-1030'), os.stat('/proc/self/ns/net'))
    os.umask(0o077)
    prefix = 'fjic_' if args.provider == 'casdoor' else 'fjil_'
    workspace = ROOT / (args.provider + '-work')
    active = yaml.safe_load((workspace / '.anas/state/active.yml').read_text())['active_deployment']
    deployment = workspace / '.anas/deployments' / active
    probe = ROOT / (args.provider + ('-hosting-probe' if args.hosting_only else '-probe'))
    probe.mkdir(mode=0o700, exist_ok=True)
    report = {'provider': args.provider, 'deployment': active, 'status': 'failed', 'checks': [],
              'scope': 'hosting-only' if args.hosting_only else 'IAM-and-hosting'}
    (probe / 'result.json').write_text(json.dumps(report, indent=2) + '\n')
    counter = 0

    def run(argv, data=None, check=True, timeout=180, env=None):
        nonlocal counter
        counter += 1
        result = subprocess.run(argv, input=data, capture_output=True, timeout=timeout, env=env)
        (probe / ('command-%03d.stderr' % counter)).write_bytes(result.stderr)
        if check and result.returncode:
            raise RuntimeError('fixture command failed: ' + argv[0])
        return result

    def d(*args, **kw):
        return run(['docker', '-H', SOCKET, *args], **kw)

    assert d('info', '--format', '{{.DockerRootDir}}').stdout.decode().strip() == str(ROOT / 'docker')

    def owned(name):
        info = json.loads(d('inspect', name).stdout)[0]
        assert info['Name'] == '/' + name
        path = info['Config']['Labels']['com.docker.compose.project.working_dir']
        assert path.startswith(str(workspace / '.anas/deployments') + '/')
        return info

    expected_images = {
        'forgejo': '894ebdf369d8a812dc8476bd1ea5bb81273bb3bdbdf67c5cc590ea97450b3119',
        'samba_dc': 'deb2d9c936cf23da2568237c1cdce2a0014683e387b70c5ec80f4c4a8605771b',
        'llng': 'f79cbb658aafd35c689dfb366ba2d7d8d3d41df6c90cfe3900a6501d1ed1d7e2',
        'casdoor': '50456c6d25c7350fc72cbf7de4955ec5ef29acc7381802630b9003f8f1759fc8',
    }
    report['images'] = {}
    for suffix in ['samba_dc', 'forgejo', args.provider]:
        live = owned(prefix + suffix)
        assert live['State']['Running']
        image = json.loads(d('image', 'inspect', live['Image']).stdout)[0]
        assert any(value.endswith('@sha256:' + expected_images[suffix]) for value in image.get('RepoDigests', [])), \
            'unexpected acceptance image baseline: ' + suffix
        report['images'][suffix] = {'container_image': live['Image'], 'repo_digests': image['RepoDigests'],
            'origin': 'imported dependency' if suffix == 'casdoor' else 'formal Dockerfile build'}
    proxy = owned(prefix + 'traefik')
    owned_socket_paths = {'/run/anas-fj-iam-1030-docker.sock', str(ROOT / 'docker-api.sock')}
    assert any(mount['Destination'] == '/var/run/docker.sock' and mount['Source'] in owned_socket_paths
        for mount in proxy['Mounts']), 'proxy must use the owned Docker socket'
    controller = owned(prefix + 'forgejo_actions_controller')
    assert controller['State']['Status'] == 'exited' and controller['State']['ExitCode'] == 0, \
        'fresh disabled Actions cleanup did not exit successfully'
    image = json.loads(d('image', 'inspect', controller['Image']).stdout)[0]
    assert any(value.endswith('@sha256:b123f4e8a65ef7d03546d4bf04b65373376767ad16d08dfcd7291046ce0c4e89')
        for value in image.get('RepoDigests', [])), 'unexpected controller image baseline'
    report['images']['actions_controller'] = {'container_image': controller['Image'], 'repo_digests': image['RepoDigests'],
        'origin': 'formal Dockerfile build'}

    def env(module):
        folder = deployment / 'modules' / module
        # Read Compose's decoded interpolation environment. Forgejo deliberately
        # exports only a subset to its container, excluding recovery credentials.
        output = d('compose', '-f', str(folder / 'docker-compose.yml'),
            '--env-file', str(folder / '.env'), 'config', '--environment').stdout.decode()
        return dict(row.split('=', 1) for row in output.splitlines() if '=' in row)

    forgejo, lego = env('forgejo'), env('lego')
    store = yaml.safe_load((workspace / '.anas/secrets.yml').read_text())
    password = store['secrets']['ANAS_LOCAL_ADMIN__FORGEJO__BREAK_GLASS__PASSWORD']['value']
    owner = forgejo['FORGEJO_LOCAL_ADMIN__BREAK_GLASS_USERNAME']
    auth = 'Basic ' + base64.b64encode((owner + ':' + password).encode()).decode()
    ca = Path(lego['ANAS_TLS_CERTS_DIR']) / lego['ANAS_TLS_TRUST_BUNDLE_NAME']
    leaf = Path(lego['ANAS_TLS_CERTS_DIR']) / lego['ANAS_TLS_CERT_NAME']
    tls = ssl.create_default_context(cafile=str(ca))
    resolve = socket.getaddrinfo
    socket.getaddrinfo = lambda host, port, *rest, **kw: resolve('10.253.31.2' if host.endswith('.fj-iam.test') else host, port, *rest, **kw)
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=tls))

    def api(path, method='GET', body=None, missing=False):
        request = urllib.request.Request(BASE + '/api/v1' + path, method=method,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Authorization': auth, 'Content-Type': 'application/json'})
        try:
            with client.open(request, timeout=30) as response:
                value = response.read()
                return json.loads(value) if value else None
        except urllib.error.HTTPError as error:
            if missing and error.code == 404:
                return None
            raise RuntimeError('Forgejo API failed with HTTP ' + str(error.code)) from None

    def record(name, passed=True, **facts):
        report['checks'].append({'name': name, 'passed': bool(passed), **facts})
        (probe / 'result.json').write_text(json.dumps(report, indent=2) + '\n')
        print(('PASS ' if passed else 'FAIL ') + name, flush=True)

    state_path = probe / 'actors.json'
    if state_path.exists():
        actors = json.loads(state_path.read_text())
    else:
        nonce = secrets.token_hex(4)
        actors = {'users': {role: 'fj' + role + nonce for role in ['user', 'admin', 'deny']},
                  'password': 'Forgejo-IAM-' + secrets.token_hex(20) + '!'}
        state_path.write_text(json.dumps(actors))

    def directory(action):
        owned(prefix + 'samba_dc')
        payload = dict(actors, action=action)
        return json.loads(d('exec', '-i', prefix + 'samba_dc', 'python3', '-c', DIRECTORY,
                            data=json.dumps(payload).encode()).stdout)

    def browser(case, role, expect='allowed'):
        name = 'fj-iam-browser-' + args.provider + '-' + case
        payload = {'case': case, 'provider': args.provider, 'origin': BASE, 'issuer': 'https://auth.fj-iam.test:19000',
                   'username': actors['users'][role], 'password': actors['password'], 'expect': expect,
                   'leaf_pem': leaf.read_text()}
        (probe / 'browser-input.json').write_text(json.dumps(payload))
        (probe / 'browser-result.json').unlink(missing_ok=True)
        try:
            if args.browser_ws_endpoint:
                # The browser runs on the workstation; only its small Node client
                # joins the host loopback SSH forward. Application API/directory
                # checks stay in the owned namespace and use the ordinary stack.
                browser_env = dict(os.environ, ANAS_FORGEJO_BROWSER_WS_ENDPOINT=args.browser_ws_endpoint,
                    ANAS_FORGEJO_TEST_HARNESS_ROOT=str(ROOT / 'inputs'),
                    ANAS_FORGEJO_TEST_FIXTURE_ROOT=str(probe))
                result = run(['nsenter', '--net=/proc/1/ns/net', str(ROOT / 'inputs/node'),
                    str(ROOT / 'inputs/forgejo-iam-native.mjs')], check=False, timeout=240, env=browser_env)
            else:
                result = d('run', '--rm', '--name', name, '--label', 'anas.test.run=forgejo-iam-20261030',
                    '--memory', '768m', '--cpus', '1', '--dns', '10.253.31.2',
                    '-v', str(ROOT / 'inputs') + ':/harness:ro', '-v', str(probe) + ':/fixture',
                    BROWSER_IMAGE, 'node', '/harness/forgejo-iam-native.mjs', check=False, timeout=240)
            (probe / ('browser-' + case + '.log')).write_bytes(result.stdout + result.stderr)
            value = json.loads((probe / 'browser-result.json').read_text())
            assert value['case'] == case and value['provider'] == args.provider
            shutil.copy2(probe / 'browser-result.json', probe / ('browser-' + case + '-result.json'))
            assert result.returncode == 0 and value['status'] == 'passed', 'real browser case failed: ' + case
            return value
        finally:
            d('rm', '-f', name, check=False)

    provider_stopped = False

    def hosting_smoke():
        nonce = secrets.token_hex(4)
        repo = 'acceptance-' + nonce
        folder = probe / repo
        folder.mkdir(mode=0o700)
        api('/user/repos', 'POST', {'name': repo, 'private': True, 'description': 'ANAS owned acceptance fixture'})
        askpass = folder / 'askpass'
        askpass.write_text('#!/bin/sh\ncase "$1" in *Username*) printf "%s" "$FJ_USER";; *) printf "%s" "$FJ_PASSWORD";; esac\n')
        askpass.chmod(0o700)
        key = folder / 'id_ed25519'
        run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(key)])
        api('/user/keys', 'POST', {'title': repo, 'key': key.with_suffix('.pub').read_text()})
        git_env = dict(os.environ, GIT_TERMINAL_PROMPT='0', GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL='/dev/null',
            GIT_ASKPASS=str(askpass), FJ_USER=owner, FJ_PASSWORD=password,
            GIT_SSH_COMMAND='ssh -F /dev/null -i ' + str(key) + ' -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=' + str(folder / 'known_hosts'))

        def git(*argv):
            return run(['git', '-c', 'credential.helper=', '-c', 'http.proxy=',
                '-c', 'http.curloptResolve=git.fj-iam.test:19000:10.253.31.2', '-c', 'http.sslCAInfo=' + str(ca),
                *argv], env=git_env).stdout.decode().strip()

        data = ('formal image smoke ' + nonce + '\n').encode()
        working = folder / 'working'
        git('init', '--initial-branch=main', str(working))
        (working / 'proof.txt').write_bytes(data)
        git('-C', str(working), 'add', 'proof.txt')
        git('-C', str(working), '-c', 'user.name=Acceptance', '-c', 'user.email=acceptance@fj-iam.test', 'commit', '-m', 'acceptance')
        url = BASE + '/' + owner + '/' + repo + '.git'
        git('-C', str(working), 'push', url, 'main')
        commit = git('-C', str(working), 'rev-parse', 'HEAD')
        git('clone', '--branch', 'main', url, str(folder / 'clone'))
        assert (folder / 'clone/proof.txt').read_bytes() == data
        ssh_url = 'ssh://git@10.253.31.2:2222/' + owner + '/' + repo + '.git'
        assert commit in git('ls-remote', ssh_url, 'refs/heads/main')
        record('formal-image-Git-HTTP-push-clone-and-SSH-read', repository=repo, commit=commit)
        ssh_clone = folder / 'ssh-clone'
        git('clone', '--branch', 'main', ssh_url, str(ssh_clone))
        assert (ssh_clone / 'proof.txt').read_bytes() == data
        (ssh_clone / 'ssh-proof.txt').write_bytes(data)
        git('-C', str(ssh_clone), 'add', 'ssh-proof.txt')
        git('-C', str(ssh_clone), '-c', 'user.name=Acceptance', '-c', 'user.email=acceptance@fj-iam.test',
            'commit', '-m', 'SSH acceptance')
        git('-C', str(ssh_clone), 'push', 'origin', 'HEAD:refs/heads/ssh-acceptance')
        ssh_commit = git('-C', str(ssh_clone), 'rev-parse', 'HEAD')
        assert ssh_commit in git('ls-remote', url, 'refs/heads/ssh-acceptance')
        record('formal-image-Git-SSH-clone-push-and-HTTP-cross-check', repository=repo, commit=ssh_commit)

        def raw(path, method='GET', data=None, content_type='application/octet-stream'):
            request = urllib.request.Request(BASE + path, data=data, method=method,
                headers={'Authorization': auth, 'Content-Type': content_type, 'Accept': content_type})
            with client.open(request, timeout=30) as response:
                return response.read()

        oid = hashlib.sha256(data).hexdigest()
        for operation in ['upload', 'download']:
            body = json.dumps({'operation': operation, 'transfers': ['basic'], 'objects': [{'oid': oid, 'size': len(data)}]}).encode()
            batch = json.loads(raw('/' + owner + '/' + repo + '.git/info/lfs/objects/batch', 'POST', body, 'application/vnd.git-lfs+json'))
            action = batch['objects'][0]['actions'][operation]
            assert urllib.parse.urlsplit(action['href'])[:2] == urllib.parse.urlsplit(BASE)[:2], 'LFS action left the owned origin'
            headers = dict(action.get('header', {}))
            if operation == 'upload':
                headers['Content-Type'] = 'application/octet-stream'
            request = urllib.request.Request(action['href'], headers=headers,
                method='PUT' if operation == 'upload' else 'GET', data=data if operation == 'upload' else None)
            with client.open(request, timeout=30) as response:
                value = response.read()
                if operation == 'download': assert value == data
        package = '/api/packages/' + owner + '/generic/acceptance/' + nonce + '/proof.txt'
        raw(package, 'PUT', data)
        assert raw(package) == data
        record('formal-image-LFS-and-package-bytes-roundtrip', sha256=oid)

    try:
        assert api('/version')['version'].startswith('17.0.1')
        assert api('/user')['is_admin'] is True
        record('formal-image-fresh-module-and-verified-TLS')
        record('fresh-disabled-Actions-cleanup-exits-without-controller-account',
            not any(user['login'] == 'anas_actions_controller' for user in api('/admin/users?limit=50')))
        if args.hosting_only:
            hosting_smoke()
            report['status'] = 'passed'
            return
        directory('create')
        deadline = time.monotonic() + 180
        while True:
            facts = directory('inspect')
            if all(len(value['anchor']) == 36 for value in facts.values()): break
            assert time.monotonic() < deadline, 'directory anchors did not converge'
            time.sleep(2)
        report['directory_anchors'] = {role: value['anchor'] for role, value in facts.items()}
        if api('/orgs/iam-test-org', missing=True) is None:
            api('/orgs', 'POST', {'username': 'iam-test-org', 'visibility': 'private'})
        teams = api('/orgs/iam-test-org/teams')
        for name in ['reviewers', 'manual']:
            if not any(team['name'] == name for team in teams):
                teams.append(api('/orgs/iam-test-org/teams', 'POST', {'name': name, 'permission': 'read', 'units': ['repo.code']}))
        team_ids = {team['name']: team['id'] for team in teams}
        def member(team, role):
            return any(user['login'] == actors['users'][role] for user in api('/teams/' + str(team_ids[team]) + '/members'))
        browser('normal', 'user')
        user = api('/users/' + actors['users']['user'])
        first_id = user['id']
        users = api('/admin/users?limit=50')
        match = next(item for item in users if item['id'] == first_id)
        assert match['login_name'] == facts['user']['anchor']
        record('real-IAM-normal-login-and-directory-subject')
        mapped = member('reviewers', 'user')
        record('custom-directory-group-to-team', mapped)
        api('/teams/' + str(team_ids['manual']) + '/members/' + actors['users']['user'], 'PUT')
        browser('admin', 'admin')
        was_admin = api('/users/' + actors['users']['admin'])['is_admin']
        record('real-IAM-admin-mapping', was_admin)
        browser('denied', 'deny', 'denied')
        record('real-IAM-app-group-denial', api('/users/' + actors['users']['deny'], missing=True) is None)
        directory('drop-admin')
        started = time.monotonic()
        for attempt in range(6):
            browser('downgraded-' + str(attempt + 1), 'admin')
            if not api('/users/' + actors['users']['admin'])['is_admin']: break
            time.sleep(10)
        record('real-IAM-admin-downgrade', was_admin and not api('/users/' + actors['users']['admin'])['is_admin'], elapsed=round(time.monotonic()-started, 2))
        directory('drop-reviewer')
        started = time.monotonic()
        for attempt in range(6):
            browser('group-removed-' + str(attempt + 1), 'user')
            if not mapped or not member('reviewers', 'user'): break
            time.sleep(10)
        record('real-IAM-team-removal', mapped and not member('reviewers', 'user'), elapsed=round(time.monotonic()-started, 2))
        record('manual-team-preserved', member('manual', 'user'))
        owned(prefix + args.provider)
        d('stop', '--timeout', '30', prefix + args.provider)
        provider_stopped = True
        assert not owned(prefix + args.provider)['State']['Running']
        browser('iam-down', 'user', 'unavailable')
        record('real-IAM-down-rejects-new-login')
        d('start', prefix + args.provider)
        provider_stopped = False
        deadline = time.monotonic()+180
        while True:
            try:
                with client.open('https://auth.fj-iam.test:19000/.well-known/openid-configuration', timeout=10) as response:
                    assert response.status == 200
                break
            except Exception:
                assert time.monotonic() < deadline, 'IAM did not recover'
                time.sleep(2)
        browser('recovered', 'user')
        record('real-IAM-recovery-preserves-account', api('/users/' + actors['users']['user'])['id'] == first_id)
        hosting_smoke()
        report['status'] = 'passed' if all(item['passed'] for item in report['checks']) else 'failed'
        if report['status'] != 'passed': raise RuntimeError('one or more required acceptance checks failed')
    finally:
        if provider_stopped: d('start', prefix + args.provider)
        (probe / 'result.json').write_text(json.dumps(report, indent=2)+'\n')


if __name__ == '__main__':
    main()
