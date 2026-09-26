#!/usr/bin/env python3
"""Explicit disposable-VM check of the Provider's project fence. Never use on a business host.

Covers what the fake daemon cannot: the real daemon's acceptance and refusal of
the restriction set (INCUS-R-011), the isolation tier held by instance-type
limits (INCUS-R-008/R-052), lease ownership markers and exclusive trust
(INCUS-R-106), and restriction keys that only exist on newer daemons.

The exact cloud-init identity must be passed by its owner. Uses a fresh daemon
in that VM, never the physical host daemon, and runs no Docker command. The
rootfs images are tiny measured fixtures that are never started.
"""
import argparse
import base64
import hashlib
import http.client
import io
import json
import os
from pathlib import Path
import re
import ssl
import subprocess
import sys
import tarfile
import time

ROOT = Path('/run/anas-incus-fence')
ENDPOINT = 'https://127.0.0.1:8443'
POOL = 'anas-fence-btrfs'
OTHER_POOL = 'anas-fence-other'
ADOPT, SHARED, PRIVILEGED, VM = 'anas-fence-adopt', 'anas-fence-shared', 'anas-fence-priv', 'anas-fence-vm'
PROJECTS = (ADOPT, SHARED, PRIVILEGED, VM)
PREFIX = 'anas-fence-'
INSTANCES = ('anas-fence-ct', 'anas-fence-escape', 'anas-fence-vmguest', 'anas-fence-priv-ct', 'anas-fence-local')
CERTIFICATES = ('manager', 'owner', 'second-workspace', 'shared', 'foreign', 'privileged', 'vm')

# API extensions that introduce the restriction keys handled per daemon. The
# generation named by the owner must match what the daemon advertises.
GENERATIONS = {
    '6.0': frozenset(),
    '7.0': frozenset({'projects_restricted_storage_pool_access', 'projects_restricted_image_servers'}),
    '7.5': frozenset({'projects_restricted_storage_pool_access', 'projects_restricted_image_servers',
                      'projects_restricted_virtual_machines_nesting'}),
}
RESTRICTION_EXTENSIONS = GENERATIONS['7.5']

BASE_CHECKS = (
    'daemon_generation_matches',
    'legacy_project_adopted_and_tightened',
    'inspect_rejects_relaxed_fence_read_only',
    'ensure_retightens_relaxed_fence',
    'container_tier_rejects_vm',
    'container_tier_accepts_container',
    'tier_switch_refused_by_daemon',
    'privilege_tightening_refused_by_daemon',
    'second_workspace_refused',
    'revoked_owner_still_refused',
    'unmarked_shared_project_refused',
    'vm_tier_rejects_container',
    'vm_tier_accepts_vm_request',
    'inspect_requires_exclusive_trust',
    'default_project_refused',
    'owned_lab_resources_removed',
)


def required_checks(generation):
    checks = set(BASE_CHECKS)
    if generation in ('7.0', '7.5'):
        checks |= {'image_servers_block_local_create', 'storage_pool_escape_rejected'}
    if generation == '7.5':
        checks.add('vm_nesting_rejected')
    return checks


def checks_passed(results, generation):
    passed = {item['check'] for item in results if item.get('passed') is True}
    failed = any(item.get('passed') is not True for item in results)
    return not failed and required_checks(generation).issubset(passed)


def require_vm(identity):
    if (os.geteuid() != 0 or not re.fullmatch(r'anas-incus-fence-[a-z0-9]{6}', identity)
            or Path('/var/lib/cloud/data/instance-id').read_text().strip() != identity
            or Path('/sys/class/dmi/id/sys_vendor').read_text().strip() != 'QEMU'
            or Path('/var/run/docker.sock').exists() or Path('/var/lib/docker').exists()):
        raise RuntimeError('exact disposable QEMU cloud identity without Docker is required')


def call(args, env=None, timeout=120, check=True):
    result = subprocess.run(args, stdin=subprocess.DEVNULL, env=env, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, timeout=timeout)
    if len(result.stdout) > 4 << 20 or len(result.stderr) > 64 << 10:
        raise RuntimeError('test subprocess output exceeded its limit')
    if check and result.returncode:
        raise RuntimeError('test subprocess failed: ' + Path(args[0]).name + ' ' + (args[1] if len(args) > 1 else ''))
    return result


def container_fixture(target):
    """A measured, never-started container image: metadata plus an empty rootfs."""
    with tarfile.open(target, 'w:xz') as archive:
        for name in ('rootfs', 'rootfs/dev', 'rootfs/proc', 'rootfs/sys', 'rootfs/etc'):
            item = tarfile.TarInfo(name); item.type = tarfile.DIRTYPE; item.mode = 0o755; archive.addfile(item)
        body = (b'architecture: x86_64\ncreation_date: 1790380800\nproperties:\n'
                b'  description: ANAS fence fixture, never started, not a product image\n  os: ANAS-test\n')
        item = tarfile.TarInfo('metadata.yaml'); item.mode = 0o644; item.size = len(body)
        archive.addfile(item, io.BytesIO(body))


def vm_metadata_fixture(target):
    with tarfile.open(target, 'w:xz') as archive:
        body = (b'architecture: x86_64\ncreation_date: 1790380800\nproperties:\n'
                b'  description: ANAS fence VM fixture, never started, not a product image\n  os: ANAS-test\n')
        item = tarfile.TarInfo('metadata.yaml'); item.mode = 0o644; item.size = len(body)
        archive.addfile(item, io.BytesIO(body))


def classify(status, body, needle):
    """A restriction refusal is synchronous; anything the daemon admits becomes an operation."""
    error = body.get('error', '') if isinstance(body, dict) else ''
    return {'http_status': status, 'refused': status >= 400 and needle in error,
            'admitted': status == 202 and body.get('type') == 'async'}


class Restricted:
    """A consumer that bypasses the shared client and talks to the daemon directly."""

    def __init__(self, name, server_pin):
        self.context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        self.context.check_hostname = False
        self.context.verify_mode = ssl.CERT_NONE
        self.context.load_cert_chain(str(ROOT/(name+'.crt')), str(ROOT/(name+'.key')))
        self.pin = server_pin

    def request(self, method, path, body=None):
        connection = http.client.HTTPSConnection('127.0.0.1', 8443, context=self.context, timeout=60)
        try:
            connection.connect()
            if hashlib.sha256(connection.sock.getpeercert(binary_form=True)).hexdigest() != self.pin:
                raise RuntimeError('lab daemon certificate does not match its pin')
            payload = None if body is None else json.dumps(body)
            connection.request(method, path, body=payload, headers={'Content-Type': 'application/json'})
            response = connection.getresponse()
            raw = response.read((4 << 20) + 1)
            if len(raw) > 4 << 20:
                raise RuntimeError('oversized lab response')
            return response.status, json.loads(raw)
        finally:
            connection.close()

    def wait(self, operation):
        status, body = self.request('GET', operation + '/wait?timeout=120')
        return status == 200 and body.get('metadata', {}).get('status') == 'Success', body.get('metadata', {}).get('err', '')


def instance_request(name, kind, pool=POOL, config=None):
    return {'name': name, 'type': kind, 'source': {'type': 'none'}, 'profiles': ['anas-lease'],
            'config': {'limits.cpu': '1', 'limits.memory': '512MiB', **(config or {})},
            'devices': {'root': {'type': 'disk', 'path': '/', 'pool': pool, 'size': '4GiB'}}}


def main(args):
    require_vm(args.vm_id)
    generation = args.generation
    if generation not in GENERATIONS:
        raise RuntimeError('unknown daemon generation')
    for name in ('provider', 'vm_rootfs'):
        path = Path(getattr(args, name))
        if not path.is_absolute() or not path.is_file() or path.is_symlink():
            raise RuntimeError('explicit regular precompiled test inputs required')
    if not args.vm_rootfs.endswith('.qcow2'):
        raise RuntimeError('the VM fixture rootfs must be a .qcow2 file so the CLI imports a VM image')
    report = Path(args.report_root)
    if not report.is_absolute() or report.exists() or ROOT.exists():
        raise RuntimeError('fresh report and runtime directories required')
    os.umask(0o077)
    report.mkdir(mode=0o700)
    ROOT.mkdir(mode=0o700)
    (ROOT/'admin').mkdir(mode=0o700)
    env = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': str(ROOT/'admin'), 'INCUS_CONF': str(ROOT/'admin'),
           'INCUS_DIR': '/var/lib/incus', 'INCUS_SOCKET': '/var/lib/incus/unix.socket'}
    results = []

    def record(check, passed, **fields):
        item = {'check': check, 'passed': bool(passed), **fields}
        results.append(item)
        print(json.dumps(item, sort_keys=True), flush=True)
        with (report/'checks.jsonl').open('a') as output:
            output.write(json.dumps(item, sort_keys=True) + '\n')

    def cli(*arguments, check=True, timeout=120):
        return call(['/usr/bin/incus', '--force-local', *arguments], env, check=check, timeout=timeout)

    def config(project):
        return json.loads(cli('query', '/1.0/projects/'+project).stdout)['config']

    def trusted():
        return json.loads(cli('config', 'trust', 'list', '--format=json').stdout)

    def fingerprint(name):
        pem = (ROOT/(name+'.crt')).read_bytes()
        der = base64.b64decode(b''.join(line for line in pem.splitlines() if not line.startswith(b'-----')), validate=True)
        return hashlib.sha256(der).hexdigest()

    call(['/usr/bin/systemctl', 'start', 'incus.service'])
    cli('admin', 'waitready', timeout=90)
    if [p['name'] for p in json.loads(cli('project', 'list', '--format=json').stdout)] != ['default']:
        raise RuntimeError('lab daemon must have only its default project')
    for kind in ('storage', 'image'):
        if json.loads(cli(kind, 'list', '--format=json').stdout):
            raise RuntimeError('lab daemon must have no existing pools or images')
    if json.loads(cli('list', '--format=json').stdout) or trusted():
        raise RuntimeError('lab daemon must have no instances or trusted certificates')

    server_info = json.loads(cli('query', '/1.0').stdout)
    advertised = RESTRICTION_EXTENSIONS & set(server_info.get('api_extensions') or [])
    record('daemon_generation_matches', advertised == GENERATIONS[generation],
           server_version=server_info.get('environment', {}).get('server_version'),
           restriction_extensions=sorted(advertised))

    provider_errors = report/'provider-stderr'
    provider_errors.mkdir(mode=0o700)
    try:
        cli('storage', 'create', POOL, 'btrfs', 'size=6GiB')
        cli('storage', 'create', OTHER_POOL, 'dir')
        for name in CERTIFICATES:
            call(['/usr/bin/openssl', 'req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:prime256v1',
                  '-nodes', '-keyout', str(ROOT/(name+'.key')), '-out', str(ROOT/(name+'.crt')), '-days', '1',
                  '-subj', '/CN=anas-fence-'+name], timeout=20)
        cli('config', 'trust', 'add-certificate', str(ROOT/'manager.crt'), '--name=anas-fence-manager')
        cli('config', 'set', 'core.https_address', '127.0.0.1:8443')
        server_pem = Path('/var/lib/incus/server.crt').read_bytes()
        server_der = base64.b64decode(b''.join(x for x in server_pem.splitlines() if not x.startswith(b'-----')))
        pin = hashlib.sha256(server_der).hexdigest()
        encode = lambda path: base64.b64encode(path.read_bytes()).decode()

        container_image = report/'container-fixture.tar.xz'
        container_fixture(container_image)
        vm_metadata = report/'vm-metadata.tar.xz'
        vm_metadata_fixture(vm_metadata)

        def create_project(project, extra=()):
            options = ['-c', 'features.networks=false', '-c', 'features.images=true', '-c', 'features.profiles=true']
            for key, value in extra:
                options += ['-c', key+'='+value]
            cli('project', 'create', project, *options)

        def import_image(project, vm=False):
            if vm:
                cli('image', 'import', str(vm_metadata), args.vm_rootfs, '--project', project, timeout=300)
            else:
                cli('image', 'import', str(container_image), '--project', project, timeout=300)
            images = json.loads(cli('image', 'list', '--project', project, '--format=json').stdout)
            if len(images) != 1 or images[0]['type'] != ('virtual-machine' if vm else 'container'):
                raise RuntimeError('fixture image import did not produce the expected type')
            return images[0]['fingerprint']

        def provider(operation, project, cert, isolation, image, consumer='fence_app', sandbox=None):
            provider_env = {'PATH': env['PATH'], 'INCUS_ENDPOINT': ENDPOINT, 'INCUS_SERVER_CERT_B64': base64.b64encode(server_pem).decode(),
                            'INCUS_ADMIN_CERT_B64': encode(ROOT/'manager.crt'), 'INCUS_ADMIN_KEY_B64': encode(ROOT/'manager.key'),
                            'INCUS_STORAGE_POOL': POOL, 'INCUS_NETWORK_IPV6': 'false',
                            'ANAS_RESOURCE_CLIENT_CERT': encode(ROOT/(cert+'.crt')), 'ANAS_RESOURCE_CONSUMER': consumer,
                            'ANAS_RESOURCE_SANDBOX': sandbox or project, 'ANAS_RESOURCE_INSTANCE_PREFIX': PREFIX,
                            'ANAS_RESOURCE_IMAGE_ARCHITECTURE': 'amd64', 'ANAS_RESOURCE_MAX_INSTANCES': '2',
                            'ANAS_RESOURCE_CPU': '1', 'ANAS_RESOURCE_MEMORY_MIB': '512', 'ANAS_RESOURCE_DISK_GIB': '4',
                            'ANAS_RESOURCE_IMAGE_ALLOWLIST': image}
            result = call([args.provider, operation, '--isolation', isolation], provider_env, check=False, timeout=300)
            label = '-'.join((operation, project, cert, isolation, str(len(list(provider_errors.iterdir())))))
            (provider_errors/(label+'.txt')).write_bytes(result.stderr)
            output = json.loads(result.stdout) if result.returncode == 0 and result.stdout.strip() else None
            return result.returncode, output, result.stderr.decode(errors='replace')

        ready = {'exists': True, 'ready': True, 'restricted': True, 'quota_enforced': True}

        # INCUS-R-011 / INCUS-R-106: an unmarked project that predates the
        # fence, carrying every loosened restriction this daemon accepts.
        loose = [('restricted', 'true'), ('restricted.backups', 'allow'), ('restricted.snapshots', 'allow'),
                 ('restricted.cluster.target', 'allow'), ('restricted.containers.interception', 'allow'),
                 ('restricted.containers.privilege', 'allow'), ('restricted.devices.infiniband', 'allow'),
                 ('restricted.devices.unix-hotplug', 'allow'), ('restricted.devices.proxy', 'allow'),
                 ('restricted.idmap.uid', '1000000-1065535'), ('restricted.idmap.gid', '1000000-1065535'),
                 ('restricted.devices.disk.paths', '/srv'), ('restricted.networks.uplinks', 'anas-fence-none'),
                 ('restricted.networks.zones', 'anas-fence-zone'), ('user.operator.note', 'preserve-me')]
        if 'projects_restricted_storage_pool_access' in advertised:
            loose.append(('restricted.storage-pools.access', POOL+','+OTHER_POOL))
        if 'projects_restricted_image_servers' in advertised:
            loose.append(('restricted.images.servers', 'images.example.invalid'))
        if 'projects_restricted_virtual_machines_nesting' in advertised:
            loose.append(('restricted.virtual-machines.nesting', 'allow'))
        create_project(ADOPT, loose)
        adopt_image = import_image(ADOPT)

        if 'projects_restricted_image_servers' in advertised:
            # Source reading says a non-empty server list also refuses an
            # instance from an image already in the project (no server named).
            attempt = cli('create', adopt_image, 'anas-fence-local', '--project', ADOPT, '--storage', POOL, check=False)
            leaked = json.loads(cli('list', '--project', ADOPT, '--format=json').stdout)
            record('image_servers_block_local_create', attempt.returncode != 0
                   and "isn't allowed in this project" in attempt.stderr.decode(errors='replace') and not leaked,
                   daemon_error=attempt.stderr.decode(errors='replace').strip()[-200:])

        code, output, _ = provider('ensure', ADOPT, 'owner', 'container', adopt_image)
        adopted = config(ADOPT)
        expected = {'restricted.backups': 'block', 'restricted.snapshots': 'block', 'restricted.cluster.target': 'block',
                    'restricted.containers.interception': 'block', 'restricted.containers.privilege': 'unprivileged',
                    'restricted.devices.infiniband': 'block', 'restricted.devices.unix-hotplug': 'block',
                    'restricted.devices.proxy': 'block', 'limits.containers': '2', 'limits.virtual-machines': '0',
                    'user.anas.consumer': 'fence_app', 'user.anas.sandbox': ADOPT,
                    'user.anas.lease_credential': fingerprint('owner'), 'user.operator.note': 'preserve-me'}
        if 'projects_restricted_storage_pool_access' in advertised:
            expected['restricted.storage-pools.access'] = POOL
        if 'projects_restricted_virtual_machines_nesting' in advertised:
            expected['restricted.virtual-machines.nesting'] = 'block'
        cleared = ('restricted.idmap.uid', 'restricted.idmap.gid', 'restricted.devices.disk.paths',
                   'restricted.networks.uplinks', 'restricted.networks.zones', 'restricted.images.servers')
        mismatched = sorted(key for key, value in expected.items() if adopted.get(key) != value)
        survived = sorted(key for key in cleared if key in adopted)
        record('legacy_project_adopted_and_tightened', code == 0 and output == ready and not mismatched and not survived,
               provider_exit=code, mismatched=mismatched, survived=survived)

        cli('project', 'set', ADOPT, 'restricted.containers.interception=allow')
        before = config(ADOPT)
        code, output, _ = provider('inspect', ADOPT, 'owner', 'container', adopt_image)
        record('inspect_rejects_relaxed_fence_read_only', code == 0 and output is not None and output['exists']
               and not output['ready'] and config(ADOPT) == before, provider_exit=code, result=output)
        code, output, _ = provider('ensure', ADOPT, 'owner', 'container', adopt_image)
        record('ensure_retightens_relaxed_fence', code == 0 and output == ready
               and config(ADOPT).get('restricted.containers.interception') == 'block', provider_exit=code)

        # INCUS-R-008 / INCUS-R-052: the tier lives on the project.
        owner = Restricted('owner', pin)
        status, body = owner.request('POST', '/1.0/instances?project='+ADOPT, instance_request('anas-fence-vmguest', 'virtual-machine'))
        verdict = classify(status, body, 'Reached maximum number of instances of type "virtual-machine"')
        record('container_tier_rejects_vm', verdict['refused'], **verdict)
        status, body = owner.request('POST', '/1.0/instances?project='+ADOPT, instance_request('anas-fence-ct', 'container'))
        verdict = classify(status, body, '')
        created, error = owner.wait(body['operation']) if verdict['admitted'] else (False, body.get('error', ''))
        record('container_tier_accepts_container', verdict['admitted'] and created, operation_error=error[-200:], **verdict)

        if 'projects_restricted_storage_pool_access' in advertised:
            status, body = owner.request('POST', '/1.0/instances?project='+ADOPT,
                                         instance_request('anas-fence-escape', 'container', pool=OTHER_POOL))
            verdict = classify(status, body, 'is not accessible from this project')
            record('storage_pool_escape_rejected', verdict['refused'], **verdict)
        else:
            # Evidence only: without the key a lease can move its root disk.
            status, body = owner.request('POST', '/1.0/instances?project='+ADOPT,
                                         instance_request('anas-fence-escape', 'container', pool=OTHER_POOL))
            if status == 202:
                owner.wait(body['operation'])
            print(json.dumps({'observation': 'storage_pool_escape_without_restriction', 'http_status': status}), flush=True)
            if cli('info', 'anas-fence-escape', '--project', ADOPT, check=False).returncode == 0:
                cli('delete', 'anas-fence-escape', '--project', ADOPT, '--force')

        before = config(ADOPT)
        code, _, _ = provider('ensure', ADOPT, 'owner', 'vm', adopt_image)
        attempt = cli('project', 'set', ADOPT, 'limits.containers=0', check=False)
        record('tier_switch_refused_by_daemon', code != 0 and config(ADOPT) == before and attempt.returncode != 0
               and 'is too low' in attempt.stderr.decode(errors='replace'),
               provider_exit=code, daemon_error=attempt.stderr.decode(errors='replace').strip()[-200:])
        cli('delete', 'anas-fence-ct', '--project', ADOPT, '--force')

        # INCUS-R-011: a tightened restriction that an existing instance violates.
        create_project(PRIVILEGED, [('restricted', 'true'), ('restricted.containers.privilege', 'allow')])
        privileged_image = import_image(PRIVILEGED)
        cli('create', '--empty', 'anas-fence-priv-ct', '--project', PRIVILEGED, '--storage', POOL, '-c', 'security.privileged=true')
        before = config(PRIVILEGED)
        code, _, _ = provider('ensure', PRIVILEGED, 'privileged', 'container', privileged_image)
        attempt = cli('project', 'set', PRIVILEGED, 'restricted.containers.privilege=unprivileged', check=False)
        record('privilege_tightening_refused_by_daemon', code != 0 and config(PRIVILEGED) == before
               and fingerprint('privileged') not in {c['fingerprint'] for c in trusted()}
               and 'Conflict detected' in attempt.stderr.decode(errors='replace'),
               provider_exit=code, daemon_error=attempt.stderr.decode(errors='replace').strip()[-200:])
        cli('delete', 'anas-fence-priv-ct', '--project', PRIVILEGED, '--force')

        # INCUS-R-106: a second workspace installing the same consumer.
        before = config(ADOPT)
        code, _, stderr = provider('ensure', ADOPT, 'second-workspace', 'container', adopt_image)
        record('second_workspace_refused', code != 0 and 'belongs to another lease' in stderr and config(ADOPT) == before
               and fingerprint('second-workspace') not in {c['fingerprint'] for c in trusted()}, provider_exit=code)
        code, output, _ = provider('revoke', ADOPT, 'owner', 'container', adopt_image)
        revoked = code == 0 and fingerprint('owner') not in {c['fingerprint'] for c in trusted()}
        code, _, stderr = provider('ensure', ADOPT, 'second-workspace', 'container', adopt_image)
        record('revoked_owner_still_refused', revoked and code != 0 and 'belongs to another lease' in stderr
               and config(ADOPT).get('user.anas.lease_credential') == fingerprint('owner'), provider_exit=code)

        create_project(SHARED, [('restricted', 'true')])
        shared_image = import_image(SHARED)
        cli('config', 'trust', 'add-certificate', str(ROOT/'foreign.crt'), '--name=anas-fence-foreign',
            '--restricted', '--projects', SHARED)
        before = config(SHARED)
        code, _, stderr = provider('ensure', SHARED, 'shared', 'container', shared_image)
        record('unmarked_shared_project_refused', code != 0 and 'also trusted by other restricted certificates ('
               + fingerprint('foreign')[:12] + ')' in stderr and config(SHARED) == before
               and fingerprint('shared') not in {c['fingerprint'] for c in trusted()}, provider_exit=code)
        cli('config', 'trust', 'remove', fingerprint('foreign'))

        # The VM tier, with an image of the right type that is never booted.
        create_project(VM)
        vm_image = import_image(VM, vm=True)
        code, output, _ = provider('ensure', VM, 'vm', 'vm', vm_image)
        vm_config = config(VM)
        vm_ready = (code == 0 and output == ready and vm_config.get('limits.containers') == '0'
                    and vm_config.get('limits.virtual-machines') == '2'
                    and vm_config.get('restricted.containers.privilege') == 'unprivileged')
        guest = Restricted('vm', pin)
        status, body = guest.request('POST', '/1.0/instances?project='+VM, instance_request('anas-fence-ct', 'container'))
        verdict = classify(status, body, 'Reached maximum number of instances of type "container"')
        record('vm_tier_rejects_container', vm_ready and verdict['refused'], provider_exit=code, **verdict)
        status, body = guest.request('POST', '/1.0/instances?project='+VM, instance_request('anas-fence-vmguest', 'virtual-machine'))
        verdict = classify(status, body, '')
        outcome = guest.wait(body['operation']) if verdict['admitted'] else (False, body.get('error', ''))
        # Admission past every project restriction is the property under test.
        # Whether this VM can later run depends on host virtualization.
        record('vm_tier_accepts_vm_request', verdict['admitted'], created=outcome[0], operation_error=outcome[1][-200:], **verdict)
        if cli('info', 'anas-fence-vmguest', '--project', VM, check=False).returncode == 0:
            cli('delete', 'anas-fence-vmguest', '--project', VM, '--force')
        if 'projects_restricted_virtual_machines_nesting' in advertised:
            status, body = guest.request('POST', '/1.0/instances?project='+VM,
                                         instance_request('anas-fence-vmguest', 'virtual-machine', config={'security.nesting': 'true'}))
            verdict = classify(status, body, 'Virtual machine nesting is forbidden')
            profile = json.loads(cli('query', '/1.0/profiles/anas-lease?project='+VM).stdout)
            record('vm_nesting_rejected', verdict['refused'] and profile['config'].get('security.nesting') == 'false'
                   and vm_config.get('restricted.virtual-machines.nesting') == 'block', **verdict)

        cli('config', 'trust', 'add-certificate', str(ROOT/'foreign.crt'), '--name=anas-fence-foreign',
            '--restricted', '--projects', VM)
        before = config(VM)
        code, shared_result, _ = provider('inspect', VM, 'vm', 'vm', vm_image)
        cli('config', 'trust', 'remove', fingerprint('foreign'))
        code_after, clean_result, _ = provider('inspect', VM, 'vm', 'vm', vm_image)
        record('inspect_requires_exclusive_trust', code == 0 and shared_result is not None and not shared_result['ready']
               and config(VM) == before and code_after == 0 and clean_result == ready, provider_exit=code)

        default_before = config('default')
        code, _, stderr = provider('ensure', 'default', 'owner', 'container', adopt_image)
        record('default_project_refused', code != 0 and 'not a valid lease project name' in stderr
               and config('default') == default_before, provider_exit=code)
    finally:
        # Never delete unknown objects: every name below is a fixed fixture.
        for project in PROJECTS:
            if cli('project', 'show', project, check=False).returncode:
                continue
            for instance in json.loads(cli('list', '--project', project, '--format=json').stdout):
                if instance['name'] not in INSTANCES:
                    raise RuntimeError('unexpected lab instance; refusing cleanup')
                cli('delete', instance['name'], '--project', project, '--force')
            for item in json.loads(cli('image', 'list', '--project', project, '--format=json').stdout):
                cli('image', 'delete', item['fingerprint'], '--project', project)
            for item in json.loads(cli('profile', 'list', '--project', project, '--format=json').stdout):
                if item['name'] not in ('default', 'anas-lease'):
                    raise RuntimeError('unexpected profile; refusing cleanup')
                if item['name'] == 'anas-lease':
                    cli('profile', 'delete', 'anas-lease', '--project', project)
            cli('project', 'delete', project)
            bridge = 'anas' + hashlib.sha256(project.encode()).hexdigest()[:10]
            if cli('network', 'show', bridge, check=False).returncode == 0:
                cli('network', 'delete', bridge)
        own = {fingerprint(name) for name in CERTIFICATES if (ROOT/(name+'.crt')).exists()}
        for certificate in trusted():
            if certificate['fingerprint'] not in own:
                raise RuntimeError('unexpected trusted certificate; refusing cleanup')
            cli('config', 'trust', 'remove', certificate['fingerprint'])
        for pool in (POOL, OTHER_POOL):
            if cli('storage', 'show', pool, check=False).returncode == 0:
                cli('storage', 'delete', pool)
        cli('config', 'unset', 'core.https_address', check=False)
        leftovers = {'projects': [p['name'] for p in json.loads(cli('project', 'list', '--format=json').stdout)],
                     'pools': json.loads(cli('storage', 'list', '--format=json').stdout),
                     'trust': trusted(), 'instances': json.loads(cli('list', '--all-projects', '--format=json').stdout)}
        clean = leftovers['projects'] == ['default'] and not leftovers['pools'] and not leftovers['trust'] and not leftovers['instances']
        record('owned_lab_resources_removed', clean)
    (report/'summary.json').write_text(json.dumps({'generation': generation, 'passed': checks_passed(results, generation),
                                                    'required': sorted(required_checks(generation)),
                                                    'results': results}, sort_keys=True, indent=1) + '\n')
    if not checks_passed(results, generation):
        raise RuntimeError('fence checks failed or were not all executed')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('vm-id', 'provider', 'vm-rootfs', 'report-root'):
        parser.add_argument('--'+name, required=True)
    parser.add_argument('--generation', required=True, choices=sorted(GENERATIONS))
    try:
        main(parser.parse_args())
    except Exception as exc:
        print(json.dumps({'passed': False, 'error_type': type(exc).__name__,
                          'message': str(exc) if type(exc) is RuntimeError else 'private fence harness failed'}), file=sys.stderr)
        sys.exit(1)
