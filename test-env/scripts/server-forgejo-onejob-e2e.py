#!/usr/bin/env python3
"""Real one-job controller, TLS Forgejo and Incus in a disposable QEMU VM.

No business Docker is used. Public test trust uses the production controller's
bounded stdin projection; the harness does not change the guest CA database.
Image, engine and Incus isolation remain unchanged.
"""
import argparse
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import pwd
import secrets
import shutil
import socket
import ssl
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.request


def load_helper(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


image_lab = load_helper('onejob_image_lab', 'server-incus-runner-image-e2e.py')
api_lab = load_helper('onejob_api_lab', 'server-forgejo-runner-api-e2e.py')
ROOT = Path('/run/anas-forgejo-onejob')
SUPPLY = Path('/run/anas/compute-image-supply')
PROJECT = 'anas-onejob-fixture'
POOL = 'anas-onejob-btrfs'
BASE = 'https://10.0.2.15:13001'
OWNER = 'anas-onejob-lab'
REPO = '/api/v1/repos/' + OWNER + '/onejob'
LABEL = 'anas-native:docker://public.ecr.aws/docker/library/busybox@sha256:7a3ebe5bfd1a4a19797d20b0c0bb39d44393e9a03fd852c0865b0f540d868df0'


def require_test_programs():
    # Fixture prerequisites, not host installation or a workload capability.
    for name in ('git', 'openssl', 'runuser', 'incus'):
        if shutil.which(name, path='/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin') is None:
            raise RuntimeError('one-job fixture prerequisite unavailable: '+name)


def workflow_result(row):
    """Accept explicit terminal status, never infer success from absence."""
    if not isinstance(row, dict):
        raise RuntimeError('unknown workflow status shape')
    terminal = {'success', 'failure', 'cancelled', 'skipped', 'timed_out', 'action_required'}
    status, conclusion = row.get('status'), row.get('conclusion')
    if not isinstance(status, str) or conclusion is not None and not isinstance(conclusion, str):
        raise RuntimeError('unknown workflow status shape')
    if status == 'canceled':
        status = 'cancelled'
    if conclusion == 'canceled':
        conclusion = 'cancelled'
    if status in terminal and conclusion in (None, '', status):
        return status
    if status == 'completed' and conclusion in terminal:
        return conclusion
    if status in ('waiting', 'running', 'queued', 'in_progress', 'pending', 'blocked') and conclusion in (None, ''):
        return None
    raise RuntimeError('unknown or conflicting workflow status')


def workflow_source(case):
    if case not in ('normal', 'failure', 'cancel', 'crash', 'unapproved'):
        raise RuntimeError('unknown fixed workflow case')
    delay = 120 if case == 'cancel' else 45 if case == 'crash' else 0
    code = 23 if case == 'failure' else 0
    return f'''name: ANAS native {case}
on: [push]
jobs:
  smoke:
    runs-on: anas-native
    steps:
      - name: entered-test-payload
        shell: sh
        run: |
          test ! -e /run/anas-actions-token/runner-token
          printf 'ANAS_REAL_ONEJOB_{case.upper()}\\n'
          sleep {delay}
          exit {code}
'''


def call(args, env=None, timeout=90, check=True):
    p = subprocess.run(args, stdin=subprocess.DEVNULL, capture_output=True, env=env, timeout=timeout)
    if len(p.stdout) + len(p.stderr) > 4 << 20:
        raise RuntimeError('fixture command output budget exceeded')
    if check and p.returncode:
        raise RuntimeError('fixed fixture command failed: ' + Path(args[0]).name)
    return p


def run(args):
    image_lab.require_vm(args.vm_id)
    report = Path(args.report_root)
    if report.parent != Path('/home/anas-test/verification') or report.exists() or ROOT.exists():
        raise RuntimeError('fresh dedicated report and runtime paths required')
    for value in (args.provider, args.tests, args.test2json, args.forgejo):
        path = Path(value)
        if not path.is_absolute() or path.is_symlink() or not path.is_file():
            raise RuntimeError('absolute regular fixture executables required')
    require_test_programs()
    release = image_lab.verify_export(Path(args.exported_image), args.expected_fingerprint)
    pin = args.expected_fingerprint
    with socket.socket() as probe:
        probe.bind(('10.0.2.15', 13001))
    account = pwd.getpwnam('anas-test')
    if account.pw_uid == 0:
        raise RuntimeError('ordinary fixture server account required')
    os.umask(0o077)
    report.mkdir(mode=0o700)
    os.chown(report, account.pw_uid, account.pw_gid)
    ROOT.mkdir(mode=0o700)
    env = {'PATH': '/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': str(report), 'USER': 'anas-test', 'GOMAXPROCS': '1'}

    def cli(*arguments, **options):
        return call(['incus', '--force-local', *arguments], **options)

    cli('admin', 'waitready')
    for command in (('list',), ('storage', 'list'), ('image', 'list'), ('config', 'trust', 'list')):
        if json.loads(cli(*command, '--format=json').stdout) != []:
            raise RuntimeError('one-job fixture requires an empty test daemon')
    if [p['name'] for p in json.loads(cli('project', 'list', '--format=json').stdout)] != ['default']:
        raise RuntimeError('unknown project in test daemon')

    resolution = {'reference': {'fingerprint': pin}, 'target': release['artifact']['target'], 'fingerprint': pin}
    item = {'resolution': resolution, 'release': release, 'metadata_path': str(SUPPLY/'incus.tar.xz'), 'rootfs_path': str(SUPPLY/'rootfs.squashfs')}
    descriptor = json.dumps({'version': 'anas.compute-image-supply/v1', 'images': [item]}, separators=(',', ':')).encode()+b'\n'
    if SUPPLY.exists():
        # Reuse only an exact, independently verified prior read-only staging
        # set. It is not overwritten or acquired for deletion by this test.
        if (SUPPLY.parent/'compute-image-supply.json').read_bytes() != descriptor:
            raise RuntimeError('existing supply does not match the frozen candidate')
        for name, part in zip(('incus.tar.xz', 'rootfs.squashfs'), release['artifact']['parts']):
            path = SUPPLY/name
            st = path.lstat()
            if path.is_symlink() or st.st_uid != 0 or st.st_nlink != 1 or st.st_mode & 0o222 or image_lab.sha256_file(path) != part['sha256']:
                raise RuntimeError('existing supply is not the verified read-only artifact')
    else:
        image_lab.require_staging_space(release)
        SUPPLY.mkdir(mode=0o700, parents=True)
        for name in ('incus.tar.xz', 'rootfs.squashfs'):
            shutil.copyfile(Path(args.exported_image)/name, SUPPLY/name)
            (SUPPLY/name).chmod(0o400)
        (SUPPLY.parent/'compute-image-supply.json').write_bytes(descriptor)
        (SUPPLY.parent/'compute-image-supply.json').chmod(0o400)

    server = controller = None
    completed = []
    auth = None

    def stop(process, budget=135):
        if process is None:
            return
        if process.poll() is None:
            process.terminate()
        try:
            process.wait(timeout=budget)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
            raise RuntimeError('fixture process did not stop within its cleanup budget')

    try:
        call(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=ANAS-onejob-test',
              '-addext', 'subjectAltName=IP:10.0.2.15', '-addext', 'basicConstraints=critical,CA:TRUE',
              '-addext', 'keyUsage=critical,digitalSignature,keyCertSign,cRLSign',
              '-keyout', str(report/'tls.key'), '-out', str(report/'tls.crt')])
        for name in ('tls.key', 'tls.crt'):
            os.chown(report/name, account.pw_uid, account.pw_gid)
        shutil.copyfile(report/'tls.crt', ROOT/'fixture-ca.crt')
        (ROOT/'fixture-ca.crt').chmod(0o400)
        app = report/'app.ini'
        app.write_text(f'''APP_NAME = ANAS isolated one-job fixture
RUN_USER = anas-test
RUN_MODE = prod
[database]
DB_TYPE = sqlite3
PATH = {report}/forgejo.db
[repository]
ROOT = {report}/repositories
[server]
PROTOCOL = https
HTTP_ADDR = 10.0.2.15
HTTP_PORT = 13001
ROOT_URL = {BASE}/
CERT_FILE = {report}/tls.crt
KEY_FILE = {report}/tls.key
DISABLE_SSH = true
OFFLINE_MODE = true
APP_DATA_PATH = {report}/data
[security]
INSTALL_LOCK = true
SECRET_KEY = {secrets.token_hex(32)}
INTERNAL_TOKEN = {secrets.token_hex(64)}
[service]
DISABLE_REGISTRATION = true
[mailer]
ENABLED = false
[actions]
ENABLED = true
[cron]
ENABLED = false
[log]
MODE = console
LEVEL = Error
''')
        os.chown(app, account.pw_uid, account.pw_gid)

        def forgejo(*argv):
            return call(['runuser', '-u', 'anas-test', '--', args.forgejo, *argv, '--config', str(app), '--work-path', str(report)], env=env).stdout.decode()

        forgejo('migrate')
        password = api_lab.generated_password(forgejo('admin', 'user', 'create', '--username', OWNER, '--email', OWNER+'@example.invalid', '--admin', '--random-password', '--random-password-length', '48', '--must-change-password=false'))
        auth = base64.b64encode((OWNER+':'+password).encode()).decode()
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(ROOT/'fixture-ca.crt'))))

        def api(method, path, value=None):
            request = urllib.request.Request(BASE+path, data=None if value is None else json.dumps(value).encode(), method=method,
                headers={'Authorization': 'Basic '+auth, 'Content-Type': 'application/json'})
            try:
                with opener.open(request, timeout=10) as response:
                    body = response.read((4<<20)+1)
                    if len(body) > 4<<20:
                        raise RuntimeError('fixture API response exceeds budget')
                    return json.loads(body) if body else None
            except urllib.error.HTTPError as e:
                raise RuntimeError('fixture API returned HTTP '+str(e.code)) from None

        with (report/'server.log').open('xb') as log:
            server = subprocess.Popen([args.forgejo, 'web', '--config', str(app), '--work-path', str(report)],
                user=account.pw_uid, group=account.pw_gid, extra_groups=[], env=env,
                stdin=subprocess.DEVNULL, stdout=log, stderr=log)
        deadline = time.monotonic()+60
        while True:
            if server.poll() is not None:
                raise RuntimeError('fixture Forgejo exited before readiness')
            try:
                version = api('GET', '/api/v1/version')
                if version['version'].split('+')[0] != '15.0.7':
                    raise RuntimeError('unexpected Forgejo fixture version')
                print(json.dumps({'check': 'tls_forgejo_ready', 'passed': True}), flush=True)
                break
            except (OSError, urllib.error.URLError):
                if time.monotonic() > deadline:
                    raise RuntimeError('Forgejo fixture readiness timeout')
                time.sleep(.5)
        for name in ('onejob', 'unapproved'):
            api('POST', '/api/v1/user/repos', {'name': name, 'private': True, 'auto_init': True, 'default_branch': 'main'})
            api('PATCH', '/api/v1/repos/'+OWNER+'/'+name, {'has_actions': True})

        for name in ('manager', 'consumer'):
            call(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=ANAS-onejob-'+name,
                  '-keyout', str(ROOT/(name+'.key')), '-out', str(ROOT/(name+'.crt'))])
        cli('storage', 'create', POOL, 'btrfs', 'size=12GiB')
        cli('config', 'trust', 'add-certificate', str(ROOT/'manager.crt'), '--name=anas-onejob-manager')
        cli('config', 'set', 'core.https_address', '127.0.0.1:8443')
        enc = lambda p: base64.b64encode(p.read_bytes()).decode()
        server_cert = Path('/var/lib/incus/server.crt')
        provider_env = {'PATH': env['PATH'], 'INCUS_ENDPOINT': 'https://127.0.0.1:8443', 'INCUS_SERVER_CERT_B64': enc(server_cert),
            'INCUS_ADMIN_CERT_B64': enc(ROOT/'manager.crt'), 'INCUS_ADMIN_KEY_B64': enc(ROOT/'manager.key'), 'ANAS_RESOURCE_CLIENT_CERT': enc(ROOT/'consumer.crt'),
            'INCUS_STORAGE_POOL': POOL, 'INCUS_NETWORK_IPV6': 'false', 'ANAS_RESOURCE_CONSUMER': 'onejob_fixture', 'ANAS_RESOURCE_SANDBOX': PROJECT,
            'ANAS_RESOURCE_INSTANCE_PREFIX': 'anas-fj-', 'ANAS_RESOURCE_IMAGE_ARCHITECTURE': 'amd64', 'ANAS_RESOURCE_MAX_INSTANCES': '1',
            'ANAS_RESOURCE_CPU': '1', 'ANAS_RESOURCE_MEMORY_MIB': '768', 'ANAS_RESOURCE_DISK_GIB': '8', 'ANAS_RESOURCE_IMAGE_ALLOWLIST': pin}
        answer = call([args.provider, 'ensure', '--isolation', 'container'], env=provider_env, timeout=300)
        if json.loads(answer.stdout) != {'exists': True, 'ready': True, 'restricted': True, 'quota_enforced': True}:
            raise RuntimeError('one-job lease is not ready')
        print(json.dumps({'check': 'onejob_provider_lease_ready', 'passed': True}), flush=True)
        der = base64.b64decode(b''.join(v for v in server_cert.read_bytes().splitlines() if not v.startswith(b'-----')))
        lease = {'Interface': 'incus_container', 'Endpoint': provider_env['INCUS_ENDPOINT'], 'Sandbox': PROJECT, 'InstancePrefix': 'anas-fj-', 'Profile': 'anas-lease',
            'ServerCertFingerprint': hashlib.sha256(der).hexdigest(), 'ServerCertB64': enc(server_cert), 'ClientCertB64': enc(ROOT/'consumer.crt'), 'ClientKeyB64': enc(ROOT/'consumer.key'),
            'ImageAllowlist': [pin], 'MaxInstances': 1, 'CPU': 1, 'MemoryMiB': 768, 'DiskGiB': 8}
        (ROOT/'fixture.json').write_text(json.dumps({'vm_id': args.vm_id, 'base_url': BASE, 'username': OWNER, 'password': password, 'ca': str(ROOT/'fixture-ca.crt'), 'label': LABEL, 'lease': lease}))
        child_env = {**env, 'ANAS_REQUIRE_FORGEJO_ONEJOB_NATIVE': '1', 'SSL_CERT_FILE': str(ROOT/'fixture-ca.crt')}
        def start_controller():
            with (report/'controller.log').open('ab') as log:
                # Signals target the real process, not a shell/test2json proxy.
                return subprocess.Popen([args.tests, '-test.v', '-test.run=^TestNativeOneJobControllerProcess$',
                    '-test.count=1', '-test.timeout=20m'], env=child_env, stdin=subprocess.DEVNULL, stdout=log, stderr=log)

        controller = start_controller()

        def check_controller():
            if controller.poll() is not None:
                raise RuntimeError('real controller exited before one-job completion')
            if 'fixture public TLS trust' in (report/'controller.log').read_text():
                raise RuntimeError('fixture public TLS trust preparation failed')

        def runs(repo):
            response = api('GET', repo+'/actions/runs')
            rows = response.get('workflow_runs') if isinstance(response, dict) else None
            if not isinstance(rows, list):
                raise RuntimeError('unknown workflow run response shape')
            return rows

        def current_run(repo, run_id):
            found = [row for row in runs(repo) if row.get('id') == run_id]
            if len(found) != 1:
                raise RuntimeError('workflow identity disappeared or became ambiguous')
            return found[0]

        revisions = {}

        def submit(case, repo=REPO):
            body = {'branch': 'main', 'message': 'ANAS native '+case,
                    'content': base64.b64encode(workflow_source(case).encode()).decode()}
            method = 'POST'
            if repo in revisions:
                method, body['sha'] = 'PUT', revisions[repo]
            response = api(method, repo+'/contents/.forgejo/workflows/probe.yml', body)
            revisions[repo] = response['content']['sha']
            commit = response['commit']['sha']
            deadline = time.monotonic()+60
            while time.monotonic() < deadline:
                check_controller()
                found = [row for row in runs(repo) if row.get('commit_sha') == commit]
                if len(found) == 1 and type(found[0].get('id')) is int and found[0]['id'] > 0:
                    return found[0]['id']
                if len(found) > 1:
                    raise RuntimeError('fixture commit produced ambiguous runs')
                time.sleep(2)
            raise RuntimeError('fixture workflow was not queued')

        def steps(run_id):
            # The pinned Forgejo API has no run/jobs/steps endpoint. Observe
            # only these non-secret columns in this fixture's own SQLite DB;
            # never write job status or read Runner token columns.
            with sqlite3.connect('file:'+str(report/'forgejo.db')+'?mode=ro', uri=True) as db:
                db.row_factory = sqlite3.Row
                return [dict(row) for row in db.execute('''
                    SELECT s.name, s.started, s.stopped, s.status, j.status AS job_status
                    FROM action_task_step s JOIN action_task t ON t.id=s.task_id
                    JOIN action_run_job j ON j.id=t.job_id
                    WHERE j.run_id=? AND t.id=j.task_id ORDER BY s."index"
                ''', (run_id,))]

        def instances():
            rows = json.loads(cli('list', '--project', PROJECT, '--format=json').stdout)
            if not isinstance(rows, list):
                raise RuntimeError('instance inventory is incomplete')
            return rows

        def registrations(repo):
            rows = api('GET', repo+'/actions/runners')
            if isinstance(rows, dict):
                rows = rows.get('runners')
            if not isinstance(rows, list):
                raise RuntimeError('registration inventory is incomplete')
            return rows

        def reclaimed():
            path = ROOT/'state/state.json'
            state = json.loads(path.read_text()) if path.exists() else None
            volumes = json.loads(cli('storage', 'volume', 'list', POOL, '--project', PROJECT, '--format=json').stdout)
            if not isinstance(volumes, list) or any(not isinstance(v, dict) or 'type' not in v for v in volumes):
                raise RuntimeError('root disk inventory is incomplete')
            return (state is not None and state.get('workloads') == {} and instances() == []
                    and not any(v['type'] in ('container', 'virtual-machine') for v in volumes)
                    and registrations(REPO) == [])

        def wait_running(run_id):
            deadline = time.monotonic()+180
            while time.monotonic() < deadline:
                check_controller()
                row = current_run(REPO, run_id)
                if workflow_result(row) is not None:
                    raise RuntimeError('long-running fixture finished before interruption')
                if row['status'] in ('running', 'in_progress'):
                    payload = [s for s in steps(run_id) if s.get('name') == 'entered-test-payload']
                    if len(payload) == 1 and payload[0]['started'] > 0 and payload[0]['stopped'] == 0:
                        live = instances()
                        if len(live) != 1:
                            raise RuntimeError('running job does not own exactly one instance')
                        return live[0]
                time.sleep(3)
            raise RuntimeError('fixture payload never started')

        unapproved_repo = '/api/v1/repos/'+OWNER+'/unapproved'
        unapproved_id = submit('unapproved', unapproved_repo)
        unapproved_started = time.monotonic()
        for case, expected in (('normal', 'success'), ('failure', 'failure'), ('crash', 'success'), ('cancel', 'controller-cancelled')):
            started = time.monotonic()
            run_id = submit(case)
            if case in ('cancel', 'crash'):
                original = wait_running(run_id)
                if case == 'cancel':
                    # Cancel the production controller's operation context via
                    # its real signal handler while the payload is executing.
                    # This is not described as Forgejo's separate web/UI cancel.
                    stop(controller)
                    if controller.returncode != 0 or not reclaimed():
                        raise RuntimeError('controller cancellation left resources or failed cleanup')
                    completed.append({'case': case, 'status': expected, 'signal': 'SIGTERM',
                        'seconds': round(time.monotonic()-started, 3), 'state_empty': True,
                        'instances_absent': True, 'root_disks_absent': True, 'registrations_absent': True})
                    (report/'cases.json').write_text(json.dumps(completed))
                    print(json.dumps({'check': case, 'passed': True, 'resources_reclaimed': True}), flush=True)
                    continue
                else:
                    # Deliberately lose the process, not its durable state.
                    # This is not a graceful stop being described as a crash.
                    controller.kill()
                    controller.wait(timeout=5)
                    if controller.returncode != -9:
                        raise RuntimeError('controller crash signal was not observed')
                    live = instances()
                    state = json.loads((ROOT/'state/state.json').read_text())
                    if len(live) != 1 or len(state['workloads']) != 1 or live[0]['name'] != original['name']:
                        raise RuntimeError('crash did not retain the running fixture and state')
                    controller = start_controller()
                    time.sleep(3)
                    live = instances()
                    if len(live) != 1 or (live[0]['name'], live[0]['created_at']) != (original['name'], original['created_at']):
                        raise RuntimeError('restart replaced rather than recovered the active guest')
            deadline = time.monotonic()+240
            while time.monotonic() < deadline:
                check_controller()
                row = current_run(REPO, run_id)
                observed = workflow_result(row)
                (report/'current-run.json').write_text(json.dumps({'case': case, 'id': run_id, 'status': row.get('status'), 'conclusion': row.get('conclusion')}))
                if observed is not None and observed != expected:
                    raise RuntimeError('workflow terminal result differs from the fixed case')
                if observed == expected and reclaimed():
                    if case != 'cancel':
                        payload = [s for s in steps(run_id) if s.get('name') == 'entered-test-payload']
                        if len(payload) != 1 or payload[0]['started'] <= 0 or payload[0]['stopped'] <= 0 or payload[0]['status'] != payload[0]['job_status']:
                            raise RuntimeError('terminal result did not come from the actual fixture payload')
                    break
                time.sleep(3)
            else:
                raise RuntimeError('workflow terminal state or resource cleanup timed out')
            completed.append({'case': case, 'status': expected, 'seconds': round(time.monotonic()-started, 3),
                              'state_empty': True, 'instances_absent': True, 'root_disks_absent': True, 'registrations_absent': True})
            (report/'cases.json').write_text(json.dumps(completed))
            print(json.dumps({'check': case, 'passed': True, 'resources_reclaimed': True}), flush=True)
        while time.monotonic()-unapproved_started < 31:
            time.sleep(1)
        denied = current_run(unapproved_repo, unapproved_id)
        if denied.get('status') != 'waiting' or registrations(unapproved_repo) != [] or instances() != []:
            raise RuntimeError('unapproved repository obtained a Runner or executed')
        completed.append({'case': 'unapproved', 'status': 'waiting', 'registrations_absent': True, 'instances_absent': True})
        stop(controller)
        if controller.returncode != 0:
            raise RuntimeError('controller final graceful cleanup did not succeed')
        controller = None
        summary = {'passed': True, 'checks': completed, 'forgejo': '15.0.7', 'fingerprint': pin, 'tls_verified': True, 'public_trust_via_controller_stdin': True, 'guest_system_trust_modified': False,
                   'real_job_executed': True, 'crash_matrix_executed': True, 'state_loss_executed': False,
                   'cancellation_mode': 'controller-context-SIGTERM', 'forgejo_web_cancel_executed': False,
                   'step_observation': 'fixture-SQLite-read-only'}
        (report/'summary.json').write_text(json.dumps(summary))
        print(json.dumps(summary), flush=True)
    finally:
        shutdown_errors = []
        for process, budget in ((controller, 135), (server, 15)):
            try:
                stop(process, budget)
            except (RuntimeError, subprocess.TimeoutExpired) as error:
                shutdown_errors.append(type(error).__name__)
        # Only the initially absent, fixed fixture project and its resources.
        if cli('project', 'show', PROJECT, check=False).returncode == 0:
            for instance in json.loads(cli('list', '--project', PROJECT, '--format=json').stdout):
                if not instance['name'].startswith('anas-fj-'):
                    raise RuntimeError('unknown fixture instance; cleanup refused')
                cli('delete', instance['name'], '--project', PROJECT, '--force')
            for cert in json.loads(cli('config', 'trust', 'list', '--format=json').stdout):
                if cert.get('projects') == [PROJECT]:
                    cli('config', 'trust', 'remove', cert['fingerprint'])
            for image in json.loads(cli('image', 'list', '--project', PROJECT, '--format=json').stdout):
                if image['fingerprint'] != pin:
                    raise RuntimeError('unknown image; cleanup refused')
                cli('image', 'delete', pin, '--project', PROJECT)
            if cli('profile', 'show', 'anas-lease', '--project', PROJECT, check=False).returncode == 0:
                cli('profile', 'delete', 'anas-lease', '--project', PROJECT)
            cli('project', 'delete', PROJECT)
        bridge = 'anas'+hashlib.sha256(PROJECT.encode()).hexdigest()[:10]
        if cli('network', 'show', bridge, check=False).returncode == 0:
            cli('network', 'delete', bridge)
        # The lease source fence ACL shares the bridge name; free it after the bridge.
        if cli('network', 'acl', 'show', bridge, check=False).returncode == 0:
            cli('network', 'acl', 'delete', bridge)
        if cli('storage', 'show', POOL, check=False).returncode == 0:
            cli('storage', 'delete', POOL)
        for cert in json.loads(cli('config', 'trust', 'list', '--format=json').stdout):
            if cert.get('name') == 'anas-onejob-manager':
                cli('config', 'trust', 'remove', cert['fingerprint'])
        cli('config', 'unset', 'core.https_address')
        print('ONEJOB_FIXTURE_DAEMON_RESOURCES_REMOVED', flush=True)
        if shutdown_errors:
            raise RuntimeError('fixture process cleanup required forced termination')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('vm-id', 'forgejo', 'provider', 'tests', 'test2json', 'exported-image', 'expected-fingerprint', 'report-root'):
        parser.add_argument('--'+name, required=True)
    try:
        run(parser.parse_args())
    except Exception as error:
        print(json.dumps({'passed': False, 'error_type': type(error).__name__, 'message': str(error) if type(error) is RuntimeError else 'private one-job fixture failure'}), file=sys.stderr)
        sys.exit(1)
