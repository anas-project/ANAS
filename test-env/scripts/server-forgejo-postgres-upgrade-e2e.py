#!/usr/bin/env python3
"""Disposable Forgejo 15.0.7 -> 17.0.1 / PostgreSQL upgrade and restore probe.

Run inside the dedicated network namespace created by server-isolated-docker.sh.
Only the explicitly named test daemon is accepted. No production Docker socket,
Incus daemon or production credentials are used. Evidence stays in --root.
This tests the runtime image with the cross-built product helper, not the full
ANAS apply lifecycle, IAM-provider matrix or a real Actions execution guest.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import time
import urllib.error
import urllib.request

SOCKET = '/run/anas-fj-1030-docker.sock'
BASE = 'http://127.0.0.1:13000'
USER = 'anas-forgejo-upgrade-test'
PG_IMAGE = 'ghcr.io/anas-project/anas-postgres:18.4.0-r5'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, required=True)
    parser.add_argument('--resume-lfs', action='store_true', help='resume only the recorded pre-LFS fixture failure')
    parser.add_argument('--resume-backup', action='store_true', help='resume the recorded backup authentication failure')
    parser.add_argument('--resume-upgrade', action='store_true', help='continue the recorded completed backup with the existing target container')
    args = parser.parse_args()
    root = args.root.resolve()
    assert root == Path('/home/whl/anas-forgejo-1030/fixture'), 'dedicated test root required'
    resume = args.resume_lfs or args.resume_backup or args.resume_upgrade
    assert sum([args.resume_lfs,args.resume_backup,args.resume_upgrade]) <= 1
    assert resume or not root.exists(), 'fresh fixture required'
    assert Path('/run/netns/anas-fj-1030').stat().st_ino == Path('/proc/self/ns/net').stat().st_ino, 'test network namespace required'
    root.mkdir(mode=0o700, exist_ok=resume)
    os.umask(0o077)
    docker = ['docker', '-H', 'unix://' + SOCKET]
    report = {'checks': [], 'images': {}, 'helper_sha256': hashlib.sha256((root.parent / 'inputs' / 'anas-forgejo-entrypoint').read_bytes()).hexdigest()}
    if resume:
        prior=json.loads((root/'result.json').read_text())
        expected=['start-15.0.7','old-git-http-push-and-ssh-read']
        if args.resume_backup or args.resume_upgrade: expected += ['old-lfs-and-package-upload','old-git-lfs-package-preserved']
        if args.resume_upgrade: expected += ['consistent-stopped-backup']
        recorded=[x['name'] for x in prior['checks']]
        if args.resume_upgrade:
            assert recorded[:len(expected)]==expected and recorded[len(expected):] in [[],['start-17.0.1'],['start-17.0.1','upgraded-git-lfs-package-preserved']]
        else:
            assert prior['status']=='failed' and recorded==expected
        assert prior['helper_sha256']==report['helper_sha256']
        suffix='upgrade-resume' if args.resume_upgrade else ('backup-auth' if args.resume_backup else 'lfs-415')
        (root/('result-'+suffix+'.json')).write_text(json.dumps(prior,indent=2)+'\n')
        report=prior
        report.pop('error',None)
        report['resumed_after']='test fixture corrections; original Git/LFS/package fixture preserved'
    password = (root/'password').read_text() if resume else secrets.token_hex(32)
    (root/'password').write_text(password)
    auth = 'Basic ' + base64.b64encode((USER+':'+password).encode()).decode()

    def run(argv, data=None, check=True, env=None):
        result = subprocess.run(argv, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, timeout=180)
        if check and result.returncode:
            # Never echo command arguments or HTTP/password-bearing output.
            raise RuntimeError('subprocess failed: '+argv[0]+' exit='+str(result.returncode))
        return result

    def d(*argv, data=None, check=True):
        return run(docker+list(argv), data, check)

    def record(name):
        report['checks'].append({'name': name, 'status': 'passed'})
        (root/'result.json').write_text(json.dumps(report, indent=2)+'\n')
        print('PASS', name, flush=True)

    def http(path, method='GET', body=None, json_body=None, content_type='application/octet-stream'):
        headers = {} if path == '/api/v1/version' else {'Authorization': auth}
        if '/info/lfs/' in path: headers['Accept'] = 'application/vnd.git-lfs+json'
        if json_body is not None:
            body = json.dumps(json_body).encode()
            if content_type == 'application/octet-stream': content_type = 'application/json'
        if body is not None:
            headers['Content-Type'] = content_type
        request = urllib.request.Request(BASE+path, data=body, method=method, headers=headers)
        with urllib.request.urlopen(request, timeout=20) as response:
            return response.read()

    def api(path, method='GET', body=None):
        data = http('/api/v1'+path, method, json_body=body)
        return json.loads(data) if data else None

    def start(version, existing=False):
        image = 'anas-forgejo-test:'+version
        if not existing:
            d('run', '-d', '--name', 'fj-app', '--network', 'fj-upgrade', '--read-only', '--tmpfs', '/tmp:rw,noexec,nosuid,size=128m',
              '-p', '127.0.0.1:13000:3000', '-p', '127.0.0.1:12222:2222', '--env-file', str(root/'forgejo.env'),
              '-v', 'fj-data:/var/lib/gitea', '-v', 'fj-config:/etc/gitea', image)
        else:
            current=d('inspect','fj-app','--format','{{.State.Status}}').stdout.decode().strip()
            assert current in ['running','exited']
            if current=='exited': d('start','fj-app')
        deadline=time.monotonic()+600
        while time.monotonic()<deadline:
            try:
                version_value = api('/version')['version']
                if version_value.startswith(version):
                    record('start-'+version); return
            except Exception:
                pass
            time.sleep(1)
        (root/('startup-'+version+'.log')).write_bytes(d('logs','fj-app',check=False).stderr)
        raise RuntimeError('Forgejo startup failed '+version)

    def helper(command, payload):
        return d('exec','-i','--user','1000:1000','fj-app','/usr/local/bin/anas-forgejo-entrypoint',command,
                 data=json.dumps(payload).encode())

    def git(*args):
        env = dict(os.environ, GIT_ASKPASS=str(root/'askpass'), GIT_TERMINAL_PROMPT='0', FJ_TEST_PASSWORD=password,
                   GIT_SSH_COMMAND='ssh -i '+str(root/'id_ed25519')+' -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile='+str(root/'known_hosts'))
        return run(['git','-c','credential.helper=']+list(args),env=env)

    try:
        daemon_root = d('info','--format','{{.DockerRootDir}}').stdout.decode().strip()
        assert daemon_root == '/home/whl/anas-forgejo-1030/docker', 'wrong Docker daemon'
        if not resume:
            for image in ['codeberg.org/forgejo/forgejo:15.0.7-rootless','codeberg.org/forgejo/forgejo:17.0.1-rootless',PG_IMAGE]:
                report['images'][image] = json.loads(d('image','inspect',image,'--format','{{json .RepoDigests}}').stdout)
            for version in ['15.0.7','17.0.1']:
                recipe = ('FROM codeberg.org/forgejo/forgejo:'+version+'-rootless\nCOPY --chmod=0755 anas-forgejo-entrypoint /usr/local/bin/anas-forgejo-entrypoint\nUSER root\nENTRYPOINT ["/usr/local/bin/anas-forgejo-entrypoint"]\nCMD ["/usr/local/bin/forgejo", "web"]\n')
                (root.parent/'inputs'/'Dockerfile.runtime-test').write_text(recipe)
                d('build','-t','anas-forgejo-test:'+version,'-f',str(root.parent/'inputs'/'Dockerfile.runtime-test'),str(root.parent/'inputs'))
            d('network','create','fj-upgrade')
            db_password=secrets.token_hex(24)
            (root/'postgres.env').write_text('POSTGRES_USER=forgejo\nPOSTGRES_DB=forgejo\nPOSTGRES_PASSWORD='+db_password+'\n')
            d('run','-d','--name','fj-db','--network','fj-upgrade','--env-file',str(root/'postgres.env'),'-v','fj-pg:/var/lib/postgresql',PG_IMAGE)
            for _ in range(60):
                if d('exec','fj-db','pg_isready','-U','forgejo',check=False).returncode==0: break
                time.sleep(1)
            else: raise RuntimeError('PostgreSQL startup failed')
            settings={
                'GITEA__database__DB_TYPE':'postgres','GITEA__database__HOST':'fj-db:5432','GITEA__database__NAME':'forgejo',
                'GITEA__database__USER':'forgejo','GITEA__database__PASSWD':db_password,'GITEA__database__SSL_MODE':'disable',
                'GITEA__security__INSTALL_LOCK':'true','GITEA__security__SECRET_KEY':secrets.token_hex(32),
                'GITEA__server__ROOT_URL':BASE+'/', 'GITEA__server__DOMAIN':'127.0.0.1', 'GITEA__server__SSH_DOMAIN':'127.0.0.1',
                'GITEA__server__SSH_PORT':'12222','GITEA__server__LFS_START_SERVER':'true',
                'GITEA__server__LFS_JWT_SECRET':base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip('='),
                'GITEA__actions__ENABLED':'true','GITEA__service__DISABLE_REGISTRATION':'true','GITEA__service__ALLOW_ONLY_EXTERNAL_REGISTRATION':'true',
                'GITEA__oauth2_client__ENABLE_AUTO_REGISTRATION':'true','GITEA__oauth2_client__ACCOUNT_LINKING':'disabled',
                'GITEA__oauth2_client__USERNAME':'nickname',
            }
            (root/'forgejo.env').write_text(''.join(k+'='+v+'\n' for k,v in settings.items()))
            (root/'askpass').write_text('#!/bin/sh\ncase "$1" in *Username*) echo '+USER+';; *) printf "%s\\n" "$FJ_TEST_PASSWORD";; esac\n')
            (root/'askpass').chmod(0o700)
            start('15.0.7')
            helper('local-admin',{'username':USER,'email':USER+'@example.test','password':password})
            api('/user/repos','POST',{'name':'upgrade-proof','private':True,'auto_init':True,'default_branch':'main'})
            git('clone',BASE+'/'+USER+'/upgrade-proof.git',str(root/'git'))
            (root/'git'/'upgrade.txt').write_text('PostgreSQL upgrade fixture\n')
            git('-C',str(root/'git'),'add','upgrade.txt')
            git('-C',str(root/'git'),'-c','user.name=Upgrade Test','-c','user.email=upgrade@example.test','commit','-m','upgrade fixture')
            git('-C',str(root/'git'),'push','origin','main')
            commit=git('-C',str(root/'git'),'rev-parse','HEAD').stdout.decode().strip()
            run(['ssh-keygen','-q','-t','ed25519','-N','','-f',str(root/'id_ed25519')])
            api('/user/keys','POST',{'title':'upgrade-fixture','key':(root/'id_ed25519.pub').read_text()})
            ssh_url='ssh://git@127.0.0.1:12222/'+USER+'/upgrade-proof.git'
            assert commit in git('ls-remote',ssh_url,'refs/heads/main').stdout.decode()
            record('old-git-http-push-and-ssh-read')
        else:
            if args.resume_upgrade:
                assert d('inspect','fj-app','--format','{{.Config.Image}}').stdout.decode().strip() == 'anas-forgejo-test:17.0.1'
            elif args.resume_backup:
                assert d('inspect','fj-app','--format','{{.Config.Image}} {{.State.Status}}').stdout.decode().strip() == 'anas-forgejo-test:15.0.7 exited'
            else: assert api('/version')['version'].startswith('15.0.7')
            commit=git('-C',str(root/'git'),'rev-parse','HEAD').stdout.decode().strip()
            ssh_url='ssh://git@127.0.0.1:12222/'+USER+'/upgrade-proof.git'
        lfs=b'fixture LFS bytes\n'; oid=hashlib.sha256(lfs).hexdigest()
        lfs_path='/'+USER+'/upgrade-proof.git/info/lfs/objects/batch'
        package=b'fixture package bytes\n'
        package_path='/api/packages/'+USER+'/generic/upgrade/1.0.0/proof.txt'
        if not (args.resume_backup or args.resume_upgrade):
            batch=json.loads(http(lfs_path,'POST',json_body={'operation':'upload','transfers':['basic'],'objects':[{'oid':oid,'size':len(lfs)}]},content_type='application/vnd.git-lfs+json'))
            action=batch['objects'][0]['actions']['upload']
            req=urllib.request.Request(action['href'],data=lfs,method='PUT',headers=action.get('header',{}))
            urllib.request.urlopen(req,timeout=20).close()
            http(package_path,'PUT',body=package)
            record('old-lfs-and-package-upload')

        def verify(label):
            assert commit in git('ls-remote',BASE+'/'+USER+'/upgrade-proof.git','refs/heads/main').stdout.decode()
            assert commit in git('ls-remote',ssh_url,'refs/heads/main').stdout.decode()
            assert http(package_path)==package
            batch=json.loads(http(lfs_path,'POST',json_body={'operation':'download','transfers':['basic'],'objects':[{'oid':oid,'size':len(lfs)}]},content_type='application/vnd.git-lfs+json'))
            action=batch['objects'][0]['actions']['download']
            req=urllib.request.Request(action['href'],headers=action.get('header',{}))
            assert urllib.request.urlopen(req,timeout=20).read()==lfs
            record(label+'-git-lfs-package-preserved')

        if not (args.resume_backup or args.resume_upgrade):
            verify('old')
            d('stop','fj-app')
        if not args.resume_upgrade:
            (root/'before.sql').write_bytes(d('exec','fj-db','sh','-c','PGPASSWORD="$POSTGRES_PASSWORD" exec pg_dump "$@"','sh','-U','forgejo','-d','forgejo','--clean','--if-exists').stdout)
            (root/'data.tar').write_bytes(d('cp','fj-app:/var/lib/gitea/.','-').stdout)
            (root/'config.tar').write_bytes(d('cp','fj-app:/etc/gitea/.','-').stdout)
            record('consistent-stopped-backup')
            d('rm','fj-app')
        start('17.0.1',existing=args.resume_upgrade); verify('upgraded')
        # Exact prerelease rollback: restore both PostgreSQL and application
        # data/config before restarting the old binary. No schema downgrade.
        d('stop','fj-app'); d('rm','fj-app')
        # Recreate the isolated database, so tables introduced by the newer
        # version cannot survive a restore of the old snapshot.
        for sql in ['DROP DATABASE forgejo','CREATE DATABASE forgejo OWNER forgejo']:
            d('exec','fj-db','sh','-c','PGPASSWORD="$POSTGRES_PASSWORD" exec psql "$@"','sh','-v','ON_ERROR_STOP=1','-U','forgejo','-d','postgres','-c',sql)
        d('exec','-i','fj-db','sh','-c','PGPASSWORD="$POSTGRES_PASSWORD" exec psql "$@"','sh','--single-transaction','-v','ON_ERROR_STOP=1','-U','forgejo','-d','forgejo',data=(root/'before.sql').read_bytes())
        d('volume','rm','fj-data','fj-config');d('volume','create','fj-data');d('volume','create','fj-config')
        d('create','--name','fj-restore','-v','fj-data:/var/lib/gitea','-v','fj-config:/etc/gitea','anas-forgejo-test:15.0.7')
        d('cp','-','fj-restore:/var/lib/gitea',data=(root/'data.tar').read_bytes())
        d('cp','-','fj-restore:/etc/gitea',data=(root/'config.tar').read_bytes());d('rm','fj-restore')
        start('15.0.7');verify('restored')
        d('stop','fj-app');d('rm','fj-app');start('17.0.1');verify('reupgraded')
        report['status']='passed'; report['commit']=commit
    except Exception as exc:
        report['status']='failed'; report['error']=type(exc).__name__+': '+str(exc)
        raise
    finally:
        (root/'result.json').write_text(json.dumps(report,indent=2)+'\n')
        (root/'forgejo.log').write_bytes(d('logs','fj-app',check=False).stderr)

if __name__=='__main__':
    main()
