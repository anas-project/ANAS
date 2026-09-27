#!/usr/bin/env python3
"""Import actual baked Runner bytes through the Provider and boot its lease.

Only the owner's exact disposable QEMU cloud instance is accepted. No package
installation, Docker operation, remote URL, or production catalog write occurs.
The expected fingerprint must be supplied from the separately recorded bake.
This smoke test is not a real Forgejo job or a signed image release.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys

ROOT = Path('/run/anas-incus-runner-image')
SUPPLY = Path('/run/anas/compute-image-supply')
PROJECT = 'anas-runner-image'
POOL = 'anas-runner-image-btrfs'
STAGING_HEADROOM = 16 << 20
RUNNER_PACKAGE = 'github.com/anas-project/ANAS/internal/computeclient'
RUNNER_TEST = 'TestNativeBakedForgejoRunnerImage'
REQUIRED_RUNNER_TESTS = frozenset({RUNNER_TEST, *(RUNNER_TEST+'/'+name for name in (
    'provider-namespace-fence', 'real-runner-binary', 'one-job-interface',
    'runner-config-readable', 'engine-config-owned', 'guest-podman-namespace-policy',
    'rootless-engine-api', 'rootless-user-session', 'rootless-oci-exec-limits',
))})


def runner_events_passed(events, return_code):
    """Accept one complete execution, not a set of success-looking labels.

    This pure check is also usable for independent archived-evidence review.
    It does not execute anything, change a report or return arbitrary test text.
    """
    if type(return_code) is not int or return_code != 0 or not isinstance(events, list):
        return False
    runs, passes = set(), set()
    starts, terminal = 0, False
    for event in events:
        if not isinstance(event, dict) or event.get('Package') != RUNNER_PACKAGE or terminal:
            return False
        action, name = event.get('Action'), event.get('Test')
        if action not in ('start', 'run', 'pause', 'cont', 'output', 'pass'):
            return False  # Includes fail, skip and malformed/unknown events.
        if 'Test' in event and (not isinstance(name, str) or name not in REQUIRED_RUNNER_TESTS):
            return False
        if action == 'start':
            if name is not None or starts or runs:
                return False
            starts += 1
        elif action == 'run':
            if name is None or name in runs or RUNNER_TEST in passes:
                return False
            if name != RUNNER_TEST and RUNNER_TEST not in runs:
                return False
            runs.add(name)
        elif action == 'pass':
            if name is None:
                if passes != REQUIRED_RUNNER_TESTS:
                    return False
                terminal = True
            else:
                if name not in runs or name in passes:
                    return False
                if name == RUNNER_TEST and passes != REQUIRED_RUNNER_TESTS - {RUNNER_TEST}:
                    return False
                passes.add(name)
        elif action in ('pause', 'cont') and (name not in runs or name in passes):
            return False
    return terminal and runs == passes == REQUIRED_RUNNER_TESTS


def require_vm(identity):
    if (os.geteuid() != 0 or not re.fullmatch(r'anas-runner-bake-[a-z0-9]{6}', identity)
            or Path('/var/lib/cloud/data/instance-id').read_text().strip() != identity
            or Path('/sys/class/dmi/id/sys_vendor').read_text().strip() != 'QEMU'
            or Path('/var/run/docker.sock').exists() or Path('/var/lib/docker').exists()):
        raise RuntimeError('exact disposable QEMU builder without Docker is required')


def sha256_file(path):
    digest = hashlib.sha256()
    with path.open('rb') as source:
        while chunk := source.read(1 << 20):
            digest.update(chunk)
    return digest.hexdigest()


def verify_export(directory, expected):
    if not re.fullmatch('[a-f0-9]{64}', expected):
        raise RuntimeError('independently recorded bake fingerprint is required')
    release = json.loads((directory / 'artifact.json').read_text())
    artifact, entry = release['artifact'], release['entry']
    target = {'architecture': 'amd64', 'interface': 'incus_container'}
    if (artifact['format'] != 'split' or artifact['target'] != target
            or artifact['fingerprint'] != expected or entry['fingerprint'] != expected
            or entry['architecture'] != 'amd64' or entry['interface'] != 'incus_container'
            or entry['name'] != 'forgejo-runner' or len(artifact['parts']) != 2):
        raise RuntimeError('export does not match the independently recorded Runner bake')
    combined = hashlib.sha256()
    for part, name, role in zip(artifact['parts'], ('incus.tar.xz', 'rootfs.squashfs'), ('metadata', 'rootfs')):
        path = directory / name
        info = path.lstat()
        if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or part['role'] != role
                or info.st_size != part['size'] or sha256_file(path) != part['sha256']):
            raise RuntimeError('baked export has invalid bytes or file identity')
        with path.open('rb') as source:
            while chunk := source.read(1 << 20):
                combined.update(chunk)
    if combined.hexdigest() != expected:
        raise RuntimeError('split export does not have the frozen Incus fingerprint')
    return release


def require_staging_space(release):
    """Reject known tmpfs exhaustion before any test directory/daemon effect.

    This is a capacity observation, not a reservation against other processes.
    The owner still disposes of retained image copies after collecting reports.
    """
    sizes = [part['size'] for part in release['artifact']['parts']]
    if len(sizes) != 2 or any(type(size) is not int or size <= 0 for size in sizes):
        raise RuntimeError('positive measured split artifact sizes required')
    try:
        fs = os.statvfs('/run')
    except OSError:
        raise RuntimeError('private image staging capacity is unavailable') from None
    if fs.f_frsize <= 0 or fs.f_bavail < 0 or fs.f_bavail * fs.f_frsize < sum(sizes) + STAGING_HEADROOM:
        raise RuntimeError('insufficient private image staging capacity; retained copies require owner review')


def call(arguments, env=None, timeout=120, check=True):
    result = subprocess.run(arguments, env=env, stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if len(result.stdout) > 4 << 20 or len(result.stderr) > 64 << 10:
        raise RuntimeError('test command exceeded its output budget')
    if check and result.returncode:
        raise RuntimeError('fixed image test command failed: ' + Path(arguments[0]).name)
    return result


def emit(name, **fields):
    print(json.dumps({'check': name, 'passed': True, **fields}), flush=True)


def main(args):
    require_vm(args.vm_id)
    exported, report = Path(args.exported_image), Path(args.report_root)
    if not exported.is_absolute() or not report.is_absolute() or report.exists() or ROOT.exists() or SUPPLY.parent.exists():
        raise RuntimeError('fresh private runtime, supply and report locations required')
    for value in (args.provider, args.tests, args.test2json):
        path = Path(value)
        if not path.is_absolute() or path.is_symlink() or not path.is_file():
            raise RuntimeError('absolute regular precompiled test programs required')
    release = verify_export(exported, args.expected_fingerprint)
    require_staging_space(release)
    pin = args.expected_fingerprint
    os.umask(0o077)
    report.mkdir(mode=0o700)
    ROOT.mkdir(mode=0o700)
    (ROOT / 'admin').mkdir(mode=0o700)
    (ROOT / 'identity').write_text(args.vm_id + '\n')
    env = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': str(ROOT/'admin'),
           'INCUS_CONF': str(ROOT/'admin'), 'INCUS_SOCKET': '/var/lib/incus/unix.socket'}

    def cli(*arguments, **options):
        return call(['/usr/bin/incus', '--force-local', *arguments], env, **options)

    call(['/usr/bin/systemctl', 'start', 'incus.service'])
    cli('admin', 'waitready', timeout=60)
    for command in (('storage', 'list'), ('image', 'list'), ('list',)):
        if json.loads(cli(*command, '--format=json').stdout):
            raise RuntimeError('image gate requires an empty disposable Incus daemon')
    if [p['name'] for p in json.loads(cli('project', 'list', '--format=json').stdout)] != ['default']:
        raise RuntimeError('unexpected project in image test daemon')
    if json.loads(cli('config', 'trust', 'list', '--format=json').stdout):
        raise RuntimeError('unexpected trusted client in image test daemon')
    try:
        SUPPLY.mkdir(mode=0o700, parents=True)
        for name in ('incus.tar.xz', 'rootfs.squashfs'):
            shutil.copyfile(exported/name, SUPPLY/name)
            (SUPPLY/name).chmod(0o400)
        # Exercise the supported explicit-fingerprint branch, not a invented
        # catalog signature. The product validates the full canonical descriptor.
        resolution = {'reference': {'fingerprint': pin}, 'target': release['artifact']['target'], 'fingerprint': pin}
        image = {'resolution': resolution, 'release': release,
                 'metadata_path': str(SUPPLY/'incus.tar.xz'), 'rootfs_path': str(SUPPLY/'rootfs.squashfs')}
        descriptor = SUPPLY.parent/'compute-image-supply.json'
        descriptor.write_text(json.dumps({'version': 'anas.compute-image-supply/v1', 'images': [image]}, separators=(',', ':'))+'\n')
        descriptor.chmod(0o400)
        cli('storage', 'create', POOL, 'btrfs', 'size=12GiB')
        for name in ('manager', 'consumer'):
            call(['/usr/bin/openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(ROOT/(name+'.key')),
                  '-out', str(ROOT/(name+'.crt')), '-days', '1', '-subj', '/CN=anas-runner-image-'+name], timeout=20)
        cli('config', 'trust', 'add-certificate', str(ROOT/'manager.crt'), '--name=anas-runner-image-manager')
        cli('config', 'set', 'core.https_address', '127.0.0.1:8443')
        encode = lambda path: base64.b64encode(path.read_bytes()).decode()
        server = Path('/var/lib/incus/server.crt')
        provider_env = {'PATH': env['PATH'], 'INCUS_ENDPOINT': 'https://127.0.0.1:8443', 'INCUS_SERVER_CERT_B64': encode(server),
                        'INCUS_ADMIN_CERT_B64': encode(ROOT/'manager.crt'), 'INCUS_ADMIN_KEY_B64': encode(ROOT/'manager.key'),
                        'ANAS_RESOURCE_CLIENT_CERT': encode(ROOT/'consumer.crt'), 'INCUS_STORAGE_POOL': POOL,
                        'INCUS_NETWORK_IPV6': 'false', 'ANAS_RESOURCE_CONSUMER': 'runner_image', 'ANAS_RESOURCE_SANDBOX': PROJECT,
                        'ANAS_RESOURCE_INSTANCE_PREFIX': 'anas-bake-', 'ANAS_RESOURCE_IMAGE_ARCHITECTURE': 'amd64',
                        'ANAS_RESOURCE_MAX_INSTANCES': '1', 'ANAS_RESOURCE_CPU': '1', 'ANAS_RESOURCE_MEMORY_MIB': '768',
                        'ANAS_RESOURCE_DISK_GIB': '8', 'ANAS_RESOURCE_IMAGE_ALLOWLIST': pin}
        for index, operation in enumerate(('ensure', 'ensure', 'inspect')):
            result = call([args.provider, operation, '--isolation', 'container'], provider_env, timeout=300, check=False)
            (report/('provider-'+str(index)+'.error')).write_bytes(result.stderr)
            if result.returncode or json.loads(result.stdout) != {'exists': True, 'ready': True, 'restricted': True, 'quota_enforced': True}:
                raise RuntimeError('Provider did not confirm baked image supply and complete lease')
        emit('provider_imports_baked_bytes_and_reuses_frozen_image', fingerprint=pin)
        der = base64.b64decode(b''.join(x for x in server.read_bytes().splitlines() if not x.startswith(b'-----')))
        lease = {'Interface': 'incus_container', 'Endpoint': provider_env['INCUS_ENDPOINT'], 'Sandbox': PROJECT,
                 'InstancePrefix': 'anas-bake-', 'Profile': 'anas-lease', 'ServerCertFingerprint': hashlib.sha256(der).hexdigest(),
                 'ServerCertB64': encode(server), 'ClientCertB64': encode(ROOT/'consumer.crt'), 'ClientKeyB64': encode(ROOT/'consumer.key'),
                 'ImageAllowlist': [pin], 'MaxInstances': 1, 'CPU': 1, 'MemoryMiB': 768, 'DiskGiB': 8}
        (ROOT/'lease.json').write_text(json.dumps(lease))
        package = RUNNER_PACKAGE
        with (report/'runner-image.jsonl').open('x') as output:
            result = subprocess.run([args.test2json, '-t', '-p', package, args.tests, '-test.v=test2json', '-test.count=1',
                                     '-test.timeout=6m', '-test.run=^TestNativeBakedForgejoRunnerImage$'],
                                    env={'PATH': env['PATH'], 'GOMAXPROCS': '1', 'ANAS_REQUIRE_INCUS_RUNNER_IMAGE_NATIVE': '1'},
                                    stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT, timeout=380)
        events = [json.loads(line) for line in (report/'runner-image.jsonl').read_text().splitlines()]
        if not runner_events_passed(events, result.returncode):
            raise RuntimeError('baked Runner smoke failed, skipped or omitted a required check')
        emit('baked_runner_boot_and_engine_smoke', real_forgejo_job=False)
    finally:
        # Only the fixed objects in this new, initially empty daemon are ours.
        if cli('project', 'show', PROJECT, check=False).returncode == 0:
            for instance in json.loads(cli('list', '--project', PROJECT, '--format=json').stdout):
                if instance['name'] != 'anas-bake-job':
                    raise RuntimeError('unknown instance; cleanup refused')
                cli('delete', instance['name'], '--project', PROJECT, '--force')
            for certificate in json.loads(cli('config', 'trust', 'list', '--format=json').stdout):
                if certificate.get('projects') == [PROJECT]:
                    cli('config', 'trust', 'remove', certificate['fingerprint'])
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
        for certificate in json.loads(cli('config', 'trust', 'list', '--format=json').stdout):
            if certificate.get('name') == 'anas-runner-image-manager':
                cli('config', 'trust', 'remove', certificate['fingerprint'])
        cli('config', 'unset', 'core.https_address')
        # This does not claim that staging files, private inputs or the VM
        # have been removed. Their owner controls evidence collection/teardown.
        emit('owned_baked_image_daemon_resources_removed')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('vm-id', 'provider', 'tests', 'test2json', 'exported-image', 'expected-fingerprint', 'report-root'):
        parser.add_argument('--'+name, required=True)
    try:
        main(parser.parse_args())
    except Exception as exc:
        print(json.dumps({'passed': False, 'error_type': type(exc).__name__,
                          'message': str(exc) if type(exc) is RuntimeError else 'private Runner image harness failed'}), file=sys.stderr)
        sys.exit(1)
