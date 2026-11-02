#!/usr/bin/env python3
"""Real one-job controller, TLS Forgejo and Incus in a disposable QEMU VM.

No business Docker is used. Public test trust uses the production controller's
bounded stdin projection; the harness does not change the guest CA database.
Image, engine and Incus isolation remain unchanged.
"""
import argparse
import base64
from datetime import datetime
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
import subprocess
import sys
import time
import traceback
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
POSTGRES = Path('/run/anas-forgejo-onejob-postgres')
FORGEJO_VERSION = '17.0.1'
SERVER_STOP_SECONDS = 150  # Match the Module's declared stop_grace_period.
BASE = 'https://10.0.2.15:13001'
OWNER = 'anas-onejob-lab'
REPO = '/api/v1/repos/' + OWNER + '/onejob'
LABEL = 'anas-native:docker://public.ecr.aws/docker/library/busybox@sha256:7a3ebe5bfd1a4a19797d20b0c0bb39d44393e9a03fd852c0865b0f540d868df0'


def require_test_programs():
    # Fixture prerequisites, not host installation or a workload capability.
    for name in ('git', 'openssl', 'runuser', 'incus', 'pg_config', 'psql'):
        if shutil.which(name, path='/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin') is None:
            raise RuntimeError('one-job fixture prerequisite unavailable: '+name)


def fixture_job_steps(api, run_id):
    """Use the pinned public API; never inspect a database or Runner secrets."""
    jobs = api('GET', REPO+'/actions/runs/'+str(run_id)+'/jobs')
    if not isinstance(jobs, list) or len(jobs) > 1:
        raise RuntimeError('unexpected one-job inventory')
    if not jobs:
        return []
    ident = jobs[0].get('id') if isinstance(jobs[0], dict) else None
    if type(ident) is not int or ident <= 0:
        raise RuntimeError('invalid job identity')
    job = api('GET', REPO+'/actions/jobs/'+str(ident))
    if not isinstance(job, dict) or job.get('id') != ident or job.get('run_id') != run_id:
        raise RuntimeError('job identity mismatch')
    if not isinstance(job.get('status'), str):
        raise RuntimeError('invalid job status')
    rows = job.get('steps')
    if rows is None:
        return []
    if not isinstance(rows, list):
        raise RuntimeError('invalid step inventory')
    result = []
    for row in rows:
        if not isinstance(row, dict) or not isinstance(row.get('name'), str):
            raise RuntimeError('invalid job step')
        item = dict(row)
        item['job_status'] = job['status']
        for name in ('started', 'stopped'):
            if name not in row:
                raise RuntimeError('missing step timestamp')
            value = row[name]
            # Forgejo 17 uses optional.Option[time.Time]: unset dates are null.
            # They mean not observed, never proof that payload execution began.
            if value is None:
                item[name] = 0
                continue
            if not isinstance(value, str):
                raise RuntimeError('invalid step timestamp')
            try:
                instant = datetime.fromisoformat(value.replace('Z', '+00:00'))
                if instant.tzinfo is None:
                    raise ValueError('timezone required')
                item[name] = 0 if instant.year == 1 else instant.timestamp()
            except ValueError as error:
                raise RuntimeError('invalid step timestamp') from error
        result.append(item)
    return result


def fixture_registrations(api, repo, org_scope=False):
    # Repository lists are scope-local unless visible=true; an org Runner must
    # be observed at the organization endpoint when confirming reclamation.
    scope = '/api/v1/orgs/anas-onejob-org' if org_scope and repo == REPO else repo
    rows = api('GET', scope+'/actions/runners')
    if isinstance(rows, dict):
        rows = rows.get('runners')
    if not isinstance(rows, list):
        raise RuntimeError('registration inventory is incomplete')
    return rows


def postgres_command(program, *arguments, check=True):
    bindir = Path(call(['pg_config', '--bindir']).stdout.decode().strip())
    if not bindir.is_absolute() or not (bindir/program).is_file():
        raise RuntimeError('PostgreSQL server tools unavailable')
    return call(['runuser', '-u', 'postgres', '--', str(bindir/program), *arguments], check=check)


def process_sample(process):
    if process.poll() is not None:
        raise RuntimeError('observed process exited')
    fields = Path('/proc/'+str(process.pid)+'/stat').read_text().rsplit(')', 1)[1].split()
    ticks = sum(int(fields[n]) for n in (11, 12, 13, 14))
    rss = int(fields[21])*os.sysconf('SC_PAGE_SIZE')
    if ticks < 0 or rss <= 0:
        raise RuntimeError('invalid process resource sample')
    return {'at': time.monotonic(), 'ticks_including_reaped_children': ticks, 'rss_bytes': rss}


def resource_metrics(samples, minimum_seconds):
    if len(samples) < 2:
        raise RuntimeError('resource observation is incomplete')
    elapsed = samples[-1]['at']-samples[0]['at']
    ticks = samples[-1]['ticks_including_reaped_children']-samples[0]['ticks_including_reaped_children']
    if elapsed < minimum_seconds or ticks < 0:
        raise RuntimeError('resource observation duration or counter is invalid')
    return {'duration_seconds': elapsed, 'max_rss_bytes': max(s['rss_bytes'] for s in samples),
            'average_single_core_cpu_percent': 100*ticks/os.sysconf('SC_CLK_TCK')/elapsed}


def validate_idle_metrics(samples, queue_times, wall_start, wall_end):
    metrics = resource_metrics(samples, 1800)
    stamps = [n for n in queue_times if wall_start <= n <= wall_end]
    if len(stamps) < 2 or any(b <= a for a, b in zip(stamps, stamps[1:])):
        raise RuntimeError('actual queue observation is incomplete')
    intervals = [(b-a)/1e9 for a, b in zip(stamps, stamps[1:])]
    metrics.update(queue_requests=len(stamps), queue_requests_per_second=len(stamps)/metrics['duration_seconds'],
                   minimum_queue_interval_seconds=min(intervals))
    if (metrics['max_rss_bytes'] > 128<<20 or metrics['average_single_core_cpu_percent'] > 1
            or metrics['queue_requests_per_second'] > .1 or min(intervals) < 10):
        raise RuntimeError('idle resource or queue frequency limit exceeded')
    return metrics


def fixture_network(waiting_ttl=False):
    # The synthetic forge runs on this disposable execution host, unlike the
    # product's Module endpoint behind Traefik. Declare that fixture placement
    # explicitly. The waiting test deliberately withholds this access.
    return {'egress': 'internet' if waiting_ttl else 'internet_lan_host', 'ingress': 'none'}


def idle_log_healthy(text):
    # runControllerLoop reports failed reconciliations with this fixed prefix.
    # Request timestamps alone must not turn an unavailable queue into a pass.
    return 'anas-forgejo-actions: reconcile:' not in text


def runner_inventory_ready(rows, isolation):
    """An in-flight CLI row is not proof of absence or verified VM policy."""
    if not isinstance(rows, list):
        raise RuntimeError('instance inventory is incomplete')
    expected = 'virtual-machine' if isolation == 'vm' else 'container'
    for row in rows:
        if not isinstance(row, dict):
            raise RuntimeError('invalid instance inventory row')
        if row.get('type') is None:
            return False
        if row['type'] != expected:
            raise RuntimeError('actual Runner instance type differs from the selected isolation')
        if isolation == 'vm':
            config = row.get('config')
            if config is None:
                config = row.get('expanded_config')
            if config is None:
                return False
            if not isinstance(config, dict) or config.get('security.secureboot') != 'true':
                raise RuntimeError('actual VM Runner does not retain Secure Boot')
    return True


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
    if case == 'build':
        return """name: ANAS native container build
on: [push]
jobs:
  smoke:
    runs-on: anas-native
    container:
      image: public.ecr.aws/docker/library/docker@sha256:f56779b4e86550493153cc8642c9c8e40b5d934e43cb5b4ea463aea5245c5c01
    steps:
      - name: entered-test-payload
        shell: sh
        run: |
          set -eu
          test ! -e /run/anas-actions-token/runner-token
          printf 'ANAS_REAL_CONTAINER_BUILD\\n' > marker
          printf 'FROM scratch\\nCOPY marker /marker\\nLABEL anas.native=onejob\\n' > Dockerfile
          DOCKER_BUILDKIT=0 docker build --pull=false --network=none -t anas-native-build:onejob .
          test "$(docker image inspect --format '{{index .Config.Labels "anas.native"}}' anas-native-build:onejob)" = onejob
          docker create --name anas-native-artifact --entrypoint /marker anas-native-build:onejob
          docker cp anas-native-artifact:/marker observed
          cmp marker observed
          docker rm --force anas-native-artifact
          docker image rm anas-native-build:onejob
"""
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
    global REPO
    container_build = getattr(args, 'container_build', False)
    org_scope = getattr(args, 'org_scope', False)
    if org_scope and not container_build:
        raise RuntimeError('organization mode is only available for the container-build fixture')
    if org_scope:
        REPO = '/api/v1/repos/anas-onejob-org/onejob'
    image_lab.require_vm(args.vm_id)
    isolation = getattr(args, 'isolation', 'container')
    interface = 'incus_vm' if isolation == 'vm' else 'incus_container'
    rootfs = 'disk.qcow2' if isolation == 'vm' else 'rootfs.squashfs'
    memory, cpu, disk = (4096, 2, 20) if isolation == 'vm' else (768, 1, 8)
    idle_baseline = getattr(args, 'idle_baseline', False)
    waiting_ttl = getattr(args, 'waiting_ttl', False)
    report = Path(args.report_root)
    if report.parent != Path('/home/anas-test/verification') or report.exists() or ROOT.exists() or POSTGRES.exists():
        raise RuntimeError('fresh dedicated report and runtime paths required')
    for value in (args.provider, args.tests, args.test2json, args.forgejo):
        path = Path(value)
        if not path.is_absolute() or path.is_symlink() or not path.is_file():
            raise RuntimeError('absolute regular fixture executables required')
    require_test_programs()
    release = image_lab.verify_export(Path(args.exported_image), args.expected_fingerprint, isolation)
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
    item = {'resolution': resolution, 'release': release, 'metadata_path': str(SUPPLY/'incus.tar.xz'), 'rootfs_path': str(SUPPLY/rootfs)}
    descriptor = json.dumps({'version': 'anas.compute-image-supply/v1', 'images': [item]}, separators=(',', ':')).encode()+b'\n'
    if SUPPLY.exists():
        # Reuse only an exact, independently verified prior read-only staging
        # set. It is not overwritten or acquired for deletion by this test.
        if (SUPPLY.parent/'compute-image-supply.json').read_bytes() != descriptor:
            raise RuntimeError('existing supply does not match the frozen candidate')
        for name, part in zip(('incus.tar.xz', rootfs), release['artifact']['parts']):
            path = SUPPLY/name
            st = path.lstat()
            if path.is_symlink() or st.st_uid != 0 or st.st_nlink != 1 or st.st_mode & 0o222 or image_lab.sha256_file(path) != part['sha256']:
                raise RuntimeError('existing supply is not the verified read-only artifact')
    else:
        image_lab.require_staging_space(release)
        SUPPLY.mkdir(mode=0o700, parents=True)
        for name in ('incus.tar.xz', rootfs):
            shutil.copyfile(Path(args.exported_image)/name, SUPPLY/name)
            (SUPPLY/name).chmod(0o400)
        (SUPPLY.parent/'compute-image-supply.json').write_bytes(descriptor)
        (SUPPLY.parent/'compute-image-supply.json').chmod(0o400)

    server = controller = None
    postgres_attempted = False
    completed = []
    pending_summary = None
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
        # A private Unix-socket-only cluster inside this disposable VM.
        # Peer auth uses the ordinary Forgejo OS account; no DB password exists.
        pg_account = pwd.getpwnam('postgres')
        POSTGRES.mkdir(mode=0o750)
        os.chown(POSTGRES, pg_account.pw_uid, account.pw_gid)
        POSTGRES.chmod(0o750)
        (POSTGRES/'socket').mkdir(mode=0o770)
        os.chown(POSTGRES/'socket', pg_account.pw_uid, account.pw_gid)
        (POSTGRES/'socket').chmod(0o770)
        postgres_command('initdb', '-D', str(POSTGRES/'data'), '--auth-local=peer', '--auth-host=reject',
                         '--locale=C', '--encoding=UTF8')
        with (POSTGRES/'data/postgresql.conf').open('a') as config:
            config.write("\nlisten_addresses = ''\nunix_socket_directories = '"+str(POSTGRES/'socket')+"'\nunix_socket_permissions = 0777\n")
        postgres_attempted = True
        postgres_command('pg_ctl', '-D', str(POSTGRES/'data'), '-l', str(POSTGRES/'server.log'), '-w', '-t', '30', 'start')
        for statement in ('CREATE ROLE "anas-test" LOGIN', 'CREATE DATABASE anas_onejob OWNER "anas-test"'):
            postgres_command('psql', '-h', str(POSTGRES/'socket'), '-d', 'postgres', '-v', 'ON_ERROR_STOP=1', '-c', statement)
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
DB_TYPE = postgres
HOST = {POSTGRES}/socket
NAME = anas_onejob
USER = anas-test
SSL_MODE = disable
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
ENABLED = {str(not idle_baseline).lower()}
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

        def start_server():
            nonlocal server
            with (report/'server.log').open('ab') as log:
                server = subprocess.Popen([args.forgejo, 'web', '--config', str(app), '--work-path', str(report)],
                    user=account.pw_uid, group=account.pw_gid, extra_groups=[], env=env,
                    stdin=subprocess.DEVNULL, stdout=log, stderr=log)
            deadline = time.monotonic()+60
            while True:
                if server.poll() is not None:
                    raise RuntimeError('fixture Forgejo exited before readiness')
                try:
                    version = api('GET', '/api/v1/version')
                    if version['version'].split('+')[0] != FORGEJO_VERSION:
                        raise RuntimeError('unexpected Forgejo fixture version')
                    print(json.dumps({'check': 'tls_forgejo_ready', 'passed': True}), flush=True)
                    break
                except (OSError, urllib.error.URLError):
                    if time.monotonic() > deadline:
                        raise RuntimeError('Forgejo fixture readiness timeout')
                    time.sleep(.5)

        start_server()
        disabled_baseline = None
        if idle_baseline:
            samples = [process_sample(server)]
            while time.monotonic()-samples[0]['at'] < 60:
                time.sleep(1)
                samples.append(process_sample(server))
            disabled_baseline = resource_metrics(samples, 60)
            (report/'forgejo-actions-disabled-samples.json').write_text(json.dumps(samples))
            stop(server, 15)
            text = app.read_text()
            if text.count('[actions]\nENABLED = false\n') != 1:
                raise RuntimeError('explicit disabled Actions baseline required')
            app.write_text(text.replace('[actions]\nENABLED = false\n', '[actions]\nENABLED = true\n'))
            start_server()
        if org_scope:
            api('POST', '/api/v1/orgs', {'username': 'anas-onejob-org', 'visibility': 'private'})
        for name in ('onejob', 'unapproved'):
            organization = org_scope and name == 'onejob'
            destination = '/api/v1/orgs/anas-onejob-org/repos' if organization else '/api/v1/user/repos'
            api('POST', destination, {'name': name, 'private': True, 'auto_init': True, 'default_branch': 'main'})
            repo_path = REPO if name == 'onejob' else '/api/v1/repos/'+OWNER+'/unapproved'
            api('PATCH', repo_path, {'has_actions': True})

        for name in ('manager', 'consumer'):
            call(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=ANAS-onejob-'+name,
                  '-keyout', str(ROOT/(name+'.key')), '-out', str(ROOT/(name+'.crt'))])
        cli('storage', 'create', POOL, 'btrfs', 'size=32GiB' if isolation == 'vm' else 'size=12GiB')
        cli('config', 'trust', 'add-certificate', str(ROOT/'manager.crt'), '--name=anas-onejob-manager')
        cli('config', 'set', 'core.https_address', '127.0.0.1:8443')
        enc = lambda p: base64.b64encode(p.read_bytes()).decode()
        server_cert = Path('/var/lib/incus/server.crt')
        provider_env = {'PATH': env['PATH'], 'INCUS_ENDPOINT': 'https://127.0.0.1:8443', 'INCUS_SERVER_CERT_B64': enc(server_cert),
            'INCUS_ADMIN_CERT_B64': enc(ROOT/'manager.crt'), 'INCUS_ADMIN_KEY_B64': enc(ROOT/'manager.key'), 'ANAS_RESOURCE_CLIENT_CERT': enc(ROOT/'consumer.crt'),
            'INCUS_STORAGE_POOL': POOL, 'INCUS_NETWORK_IPV6': 'false', 'ANAS_RESOURCE_CONSUMER': 'onejob_fixture', 'ANAS_RESOURCE_SANDBOX': PROJECT,
            'ANAS_RESOURCE_INSTANCE_PREFIX': 'anas-fj-', 'ANAS_RESOURCE_IMAGE_ARCHITECTURE': 'amd64', 'ANAS_RESOURCE_MAX_INSTANCES': '1',
            'ANAS_RESOURCE_CPU': str(cpu), 'ANAS_RESOURCE_MEMORY_MIB': str(memory), 'ANAS_RESOURCE_DISK_GIB': str(disk), 'ANAS_RESOURCE_IMAGE_ALLOWLIST': pin}
        provider_env['ANAS_RESOURCE_NETWORK'] = json.dumps(fixture_network(waiting_ttl))
        provider_env['ANAS_RESOURCE_HOST_ADDRESSES'] = '10.0.2.15/32'
        answer = call([args.provider, 'ensure', '--isolation', isolation], env=provider_env, timeout=300)
        if not image_lab.provider_confirmed(json.loads(answer.stdout), 'ensure'):
            raise RuntimeError('one-job lease is not ready')
        print(json.dumps({'check': 'onejob_provider_lease_ready', 'passed': True}), flush=True)
        der = base64.b64decode(b''.join(v for v in server_cert.read_bytes().splitlines() if not v.startswith(b'-----')))
        lease = {'Interface': interface, 'Endpoint': provider_env['INCUS_ENDPOINT'], 'Sandbox': PROJECT, 'InstancePrefix': 'anas-fj-', 'Profile': 'anas-lease',
            'ServerCertFingerprint': hashlib.sha256(der).hexdigest(), 'ServerCertB64': enc(server_cert), 'ClientCertB64': enc(ROOT/'consumer.crt'), 'ClientKeyB64': enc(ROOT/'consumer.key'),
            'ImageAllowlist': [pin], 'MaxInstances': 1, 'CPU': cpu, 'MemoryMiB': memory, 'DiskGiB': disk}
        (ROOT/'fixture.json').write_text(json.dumps({'scope_kind': 'org' if org_scope else 'repo', 'vm_id': args.vm_id, 'base_url': BASE, 'username': OWNER, 'password': password, 'ca': str(ROOT/'fixture-ca.crt'), 'label': LABEL, 'lease': lease}))
        child_env = {**env, 'ANAS_REQUIRE_FORGEJO_ONEJOB_NATIVE': '1', 'SSL_CERT_FILE': str(ROOT/'fixture-ca.crt')}
        if idle_baseline:
            child_env['ANAS_NATIVE_QUEUE_TRACE'] = '1'
        def start_controller():
            with (report/'controller.log').open('ab') as log:
                # Signals target the real process, not a shell/test2json proxy.
                return subprocess.Popen([args.tests, '-test.v', '-test.run=^TestNativeOneJobControllerProcess$',
                    '-test.count=1', '-test.timeout='+('40m' if idle_baseline else '20m')], env=child_env, stdin=subprocess.DEVNULL, stdout=log, stderr=log)

        controller = start_controller()

        def check_controller():
            if controller.poll() is not None:
                raise RuntimeError('real controller exited before one-job completion')
            if 'fixture public TLS trust' in (report/'controller.log').read_text():
                raise RuntimeError('fixture public TLS trust preparation failed')
            if idle_baseline and not idle_log_healthy((report/'controller.log').read_text()):
                raise RuntimeError('idle queue reconciliation failed')

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
            return fixture_job_steps(api, run_id)

        observed_vm_instances = set()

        def instances():
            deadline = time.monotonic()+5
            while True:
                rows = json.loads(cli('list', '--project', PROJECT, '--format=json').stdout)
                if runner_inventory_ready(rows, isolation):
                    break
                if time.monotonic() >= deadline:
                    raise RuntimeError('instance inventory did not resolve within the observation budget')
                time.sleep(.2)
            if isolation == 'vm':
                observed_vm_instances.update(row['name'] for row in rows)
            return rows

        def registrations(repo):
            return fixture_registrations(api, repo, org_scope)

        def reclaimed(allow_empty_state=False):
            path = ROOT/'state/state.json'
            state = json.loads(path.read_text()) if path.exists() else None
            volumes = json.loads(cli('storage', 'volume', 'list', POOL, '--project', PROJECT, '--format=json').stdout)
            if not isinstance(volumes, list) or any(not isinstance(v, dict) or 'type' not in v for v in volumes):
                raise RuntimeError('root disk inventory is incomplete')
            return ((state is not None or allow_empty_state) and (state is None or state.get('workloads') == {}) and instances() == []
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

        if idle_baseline:
            trace = ROOT/'queue-trace.txt'
            deadline = time.monotonic()+60
            while not trace.exists() or len(trace.read_text().splitlines()) < 2:
                check_controller()
                if time.monotonic() >= deadline:
                    raise RuntimeError('two actual reconciliation cycles were not observed')
                time.sleep(1)
            if not reclaimed(allow_empty_state=True):
                raise RuntimeError('idle prewarm resources exist after two cycles')
            samples = [process_sample(controller)]
            wall_start = time.time_ns()
            inventory_checks = 1
            next_inventory = samples[0]['at']+15
            while time.monotonic()-samples[0]['at'] < 1800:
                time.sleep(1)
                check_controller()
                samples.append(process_sample(controller))
                if samples[-1]['at'] >= next_inventory:
                    (report/'idle-controller-samples.json').write_text(json.dumps(samples))
                    if not reclaimed(allow_empty_state=True):
                        raise RuntimeError('idle Runner resources appeared')
                    inventory_checks += 1
                    next_inventory = time.monotonic()+15
            wall_end = time.time_ns()
            queue_times = [int(n) for n in trace.read_text().splitlines()]
            (report/'idle-controller-samples.json').write_text(json.dumps(samples))
            (report/'idle-queue-times.json').write_text(json.dumps({'start': wall_start, 'end': wall_end, 'requests': queue_times}))
            metrics = validate_idle_metrics(samples, queue_times, wall_start, wall_end)
            stop(controller)
            if (controller.returncode != 0 or not reclaimed(allow_empty_state=True)
                    or not idle_log_healthy((report/'controller.log').read_text())):
                raise RuntimeError('idle controller did not stop cleanly')
            controller = None
            (report/'idle-controller-samples.json').write_text(json.dumps(samples))
            summary = {'passed': True, 'forgejo': FORGEJO_VERSION, 'database': 'postgres', 'isolation': interface, 'runner_memory_mib': memory,
                       'idle_controller': metrics, 'forgejo_actions_disabled': disabled_baseline,
                       'empty_inventory_checks': inventory_checks, 'prewarm': 0,
                       'scope': OWNER+'/onejob', 'real_job_executed': False,
                       'queue_observation': 'actual transport timestamps; no URL, headers or credentials'}
            pending_summary = summary
            return

        if waiting_ttl:
            run_id = submit('normal')
            state_path = ROOT/'state/state.json'
            deadline = time.monotonic()+120
            while True:
                check_controller()
                state = json.loads(state_path.read_text()) if state_path.exists() else {}
                work = state.get('workloads', {})
                if len(work) == 1:
                    handle, original = next(iter(work.items()))
                    if original.get('phase') == 'running' and len(instances()) == 1:
                        break
                if time.monotonic() >= deadline:
                    raise RuntimeError('waiting Runner did not start')
                time.sleep(2)
            # Connectivity-only negative control: no API request, credential or
            # TLS bypass. The real controller can still reach the real forge.
            probe = cli('exec', original['instance_id'], '--project', PROJECT, '--',
                        '/usr/bin/timeout', '6', '/bin/bash', '-c', 'exec 3<>/dev/tcp/10.0.2.15/13001',
                        timeout=12, check=False)
            if probe.returncode != 124:
                raise RuntimeError('waiting control did not prove the fixture endpoint was withheld')
            created = datetime.fromisoformat(original['created_at'].replace('Z', '+00:00')).timestamp()
            observations = []
            while True:
                check_controller()
                age = time.time()-created
                state = json.loads(state_path.read_text())
                current = state.get('workloads', {}).get(handle)
                jobs = api('GET', REPO+'/actions/runs/'+str(run_id)+'/jobs')
                if (not isinstance(jobs, list) or len(jobs) != 1 or type(jobs[0].get('task_id')) is not int
                        or jobs[0]['task_id'] != 0):
                    raise RuntimeError('waiting fixture job was claimed or its identity is unknown')
                row = current_run(REPO, run_id)
                if workflow_result(row) is not None:
                    raise RuntimeError('waiting fixture unexpectedly finished')
                observations.append({'age_seconds': age, 'workload_present': current is not None,
                                     'task_id': 0, 'run_status': row.get('status')})
                (report/'waiting-ttl-observations.json').write_text(json.dumps(observations))
                if current is None:
                    if age < 600 or not reclaimed():
                        raise RuntimeError('waiting resources disappeared early or were not reclaimed')
                    retry = state.get('retry_after', {}).get(handle)
                    if not isinstance(retry, str) or datetime.fromisoformat(retry.replace('Z', '+00:00')).timestamp() <= time.time():
                        raise RuntimeError('waiting cleanup did not persist retry backoff')
                    break
                if current.get('created_at') != original['created_at'] or age > 720:
                    raise RuntimeError('waiting TTL was reset or did not reclaim resources')
                time.sleep(5)
            for _ in range(6):
                time.sleep(5)
                check_controller()
                if not reclaimed():
                    raise RuntimeError('waiting retry ignored its backoff')
            stop(controller)
            if controller.returncode != 0 or not reclaimed():
                raise RuntimeError('waiting controller did not stop cleanly')
            controller = None
            summary = {'passed': True, 'forgejo': FORGEJO_VERSION, 'database': 'postgres', 'isolation': interface, 'runner_memory_mib': memory,
                       'waiting_ttl_seconds': 600, 'observed_cleanup_age_seconds': age,
                       'unclaimed_real_job': True, 'retry_backoff_observed_seconds': 30,
                       'resources_reclaimed': True, 'guest_tcp_probe_exit': probe.returncode,
                       'control': 'internet egress withholds synthetic execution-host forge',
                       'real_payload_executed': False}
            pending_summary = summary
            return

        unapproved_repo = '/api/v1/repos/'+OWNER+'/unapproved'
        unapproved_id = submit('unapproved', unapproved_repo)
        unapproved_started = time.monotonic()
        matrix = (('build', 'success'),) if container_build else (('normal', 'success'), ('failure', 'failure'), ('crash', 'success'), ('cancel', 'controller-cancelled'))
        for case, expected in matrix:
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
                instances()  # Independently observe actual isolation while the job is active.
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
        if isolation == 'vm' and not observed_vm_instances:
            raise RuntimeError('no actual Secure Boot VM was observed during the real job')
        summary = {'passed': True, 'checks': completed, 'forgejo': FORGEJO_VERSION, 'database': 'postgres', 'isolation': interface, 'runner_memory_mib': memory, 'fingerprint': pin, 'tls_verified': True, 'public_trust_via_controller_stdin': True, 'guest_system_trust_modified': False,
                   'real_job_executed': True, 'vm_secure_boot_observed': bool(observed_vm_instances) if isolation == 'vm' else None, 'crash_matrix_executed': not container_build, 'container_build_executed': container_build, 'scope_kind': 'org' if org_scope else 'repo', 'state_loss_executed': False,
                   'cancellation_mode': None if container_build else 'controller-context-SIGTERM', 'forgejo_web_cancel_executed': False,
                   'step_observation': 'Forgejo-single-job-API'}
        pending_summary = summary
    finally:
        shutdown_errors = []
        process_outcomes = []
        for name, process, budget in (('controller', controller, 135), ('forgejo', server, SERVER_STOP_SECONDS)):
            started = time.monotonic()
            try:
                stop(process, budget)
            except (RuntimeError, subprocess.TimeoutExpired) as error:
                shutdown_errors.append(type(error).__name__)
            process_outcomes.append({'name': name, 'budget_seconds': budget,
                                     'seconds': round(time.monotonic()-started, 3),
                                     'exit_code': process.returncode if process is not None else None,
                                     'forced_kill': process is not None and process.returncode == -9})
        if postgres_attempted:
            stopped = postgres_command('pg_ctl', '-D', str(POSTGRES/'data'), '-w', '-t', '30', '-m', 'fast', 'stop', check=False)
            if stopped.returncode:
                shutdown_errors.append('PostgreSQLStopFailed')
            process_outcomes.append({'name': 'postgresql', 'exit_code': stopped.returncode})
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
        for prefix in ('lease', 'anas'):
            bridge = prefix+hashlib.sha256(PROJECT.encode()).hexdigest()[:10]
            if cli('network', 'show', bridge, check=False).returncode == 0:
                cli('network', 'delete', bridge)
            if cli('network', 'acl', 'show', bridge, check=False).returncode == 0:
                cli('network', 'acl', 'delete', bridge)
        if cli('storage', 'show', POOL, check=False).returncode == 0:
            cli('storage', 'delete', POOL)
        for cert in json.loads(cli('config', 'trust', 'list', '--format=json').stdout):
            if cert.get('name') == 'anas-onejob-manager':
                cli('config', 'trust', 'remove', cert['fingerprint'])
        cli('config', 'unset', 'core.https_address')
        print('ONEJOB_FIXTURE_DAEMON_RESOURCES_REMOVED', flush=True)
        cleanup = {'passed': not shutdown_errors, 'processes': process_outcomes, 'error_types': shutdown_errors}
        (report/'fixture-process-cleanup.json').write_text(json.dumps(cleanup))
        if pending_summary is not None:
            pending_summary['fixture_cleanup_passed'] = not shutdown_errors
            pending_summary['passed'] = not shutdown_errors
            (report/'summary.json').write_text(json.dumps(pending_summary))
            print(json.dumps(pending_summary), flush=True)
        if shutdown_errors:
            raise RuntimeError('fixture process cleanup required forced termination')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--isolation', choices=('container', 'vm'), default='container')
    for name in ('vm-id', 'forgejo', 'provider', 'tests', 'test2json', 'exported-image', 'expected-fingerprint', 'report-root'):
        parser.add_argument('--'+name, required=True)
    modes = parser.add_mutually_exclusive_group()
    parser.add_argument('--org-scope', action='store_true', help='organization scope for the container-build fixture only')
    modes.add_argument('--container-build', action='store_true', help='real secret-free image build and artifact-byte verification')
    modes.add_argument('--idle-baseline', action='store_true', help='30-minute empty queue gate with disabled-Actions baseline')
    modes.add_argument('--waiting-ttl', action='store_true', help='real 10-minute unclaimed-job cleanup and retry gate')
    try:
        run(parser.parse_args())
    except Exception as error:
        line = next((x.lineno for x in reversed(traceback.extract_tb(error.__traceback__)) if x.filename == __file__), 0)
        print(json.dumps({'passed': False, 'error_type': type(error).__name__, 'error_line': line, 'attribute': getattr(error, 'name', None) if getattr(error, 'name', None) in ('get', 'returncode', 'wait', 'poll', 'terminate', 'pid', 'stdout', 'stat', 'read_text', 'startswith') else None, 'message': str(error) if type(error) is RuntimeError else 'private one-job fixture failure'}), file=sys.stderr)
        sys.exit(1)
