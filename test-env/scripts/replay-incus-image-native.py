#!/usr/bin/env python3
"""Replay an explicitly owned image fixture without duplicating tmpfs bytes.

The initial image harness must have prepared the private frozen lease and supply
files and cleaned its daemon resources. The root/group case is a diagnostic,
never immutable-image admission. No system security setting is modified here.
"""
import argparse
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import stat
import subprocess
import sys

spec = importlib.util.spec_from_file_location('image_replay_lab', Path(__file__).with_name('server-incus-runner-image-e2e.py'))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)
ROOT = Path('/run/anas-incus-runner-image')
PROJECT = 'anas-runner-image'
POOL = 'anas-runner-image-btrfs'
CASES = {'image': ('TestNativeBakedForgejoRunnerImage', 'ANAS_REQUIRE_INCUS_RUNNER_IMAGE_NATIVE'),
         'user-config-control': ('TestNativeRunnerUserConfigControl', 'ANAS_REQUIRE_INCUS_USER_CONFIG_CONTROL'),
         'user-manager-control': ('TestNativeRunnerUserManagerControl', 'ANAS_REQUIRE_INCUS_USER_MANAGER_CONTROL'),
         'cgroup-control': ('TestNativeRunnerCgroupManagerControl', 'ANAS_REQUIRE_INCUS_CGROUP_CONTROL'),
         'root-group-control': ('TestNativeRunnerRootAndGroupControl', 'ANAS_REQUIRE_INCUS_ROOT_MODE_CONTROL')}


def private(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1 or stat.S_IMODE(info.st_mode) & 0o077 or info.st_size > 64 << 10:
        raise RuntimeError('private fixture input is unsafe')
    return path.read_bytes()


def main(args):
    lab.require_vm(args.vm_id)
    if args.case not in CASES:
        raise RuntimeError('unknown fixed native case')
    report = Path(args.report_root)
    if report.parent != Path('/home/anas-test/verification') or report.exists():
        raise RuntimeError('fresh owned fixture report required')
    for value in (args.tests, args.provider, args.test2json):
        p = Path(value)
        if not p.is_absolute() or p.is_symlink() or not p.is_file():
            raise RuntimeError('absolute regular fixture programs required')
    if private(ROOT/'identity').decode().strip() != args.vm_id:
        raise RuntimeError('retained fixture identity differs from this VM')
    lease = json.loads(private(ROOT/'lease.json'))
    if lease['Sandbox'] != PROJECT or lease['Endpoint'] != 'https://127.0.0.1:8443' or lease['Interface'] != 'incus_container' or lease['InstancePrefix'] != 'anas-bake-' or lease['CPU'] != 1 or lease['MemoryMiB'] != 768 or lease['DiskGiB'] != 8 or len(lease['ImageAllowlist']) != 1:
        raise RuntimeError('retained lease does not match the fixed native fixture')
    pin = lease['ImageAllowlist'][0]
    if args.case == 'root-group-control' and (args.vm_id != 'anas-runner-bake-uefkek' or pin != '75ec84539232bd9e1d24ac55cd8b6f7a68a65ce1a4a9ead283afe8bf509d58c5'):
        raise RuntimeError('diagnostic requires the known faulty candidate')
    encode = lambda p: base64.b64encode(private(p)).decode()
    env = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'INCUS_ENDPOINT': lease['Endpoint'], 'INCUS_SERVER_CERT_B64': lease['ServerCertB64'],
           'INCUS_ADMIN_CERT_B64': encode(ROOT/'manager.crt'), 'INCUS_ADMIN_KEY_B64': encode(ROOT/'manager.key'),
           'ANAS_RESOURCE_CLIENT_CERT': lease['ClientCertB64'], 'INCUS_STORAGE_POOL': POOL, 'INCUS_NETWORK_IPV6': 'false',
           'ANAS_RESOURCE_CONSUMER': 'runner_image', 'ANAS_RESOURCE_SANDBOX': PROJECT, 'ANAS_RESOURCE_INSTANCE_PREFIX': 'anas-bake-',
           'ANAS_RESOURCE_IMAGE_ARCHITECTURE': 'amd64', 'ANAS_RESOURCE_MAX_INSTANCES': '1', 'ANAS_RESOURCE_CPU': '1',
           'ANAS_RESOURCE_MEMORY_MIB': '768', 'ANAS_RESOURCE_DISK_GIB': '8', 'ANAS_RESOURCE_IMAGE_ALLOWLIST': pin}

    def cli(*arguments, **options):
        return lab.call(['incus', '--force-local', *arguments], **options)

    for command in (('list',), ('storage', 'list'), ('image', 'list'), ('config', 'trust', 'list')):
        if json.loads(cli(*command, '--format=json').stdout) != []:
            raise RuntimeError('replay requires an empty test daemon')
    if [p['name'] for p in json.loads(cli('project', 'list', '--format=json').stdout)] != ['default']:
        raise RuntimeError('replay would adopt another project')
    os.umask(0o077)
    report.mkdir(mode=0o700)
    passed = False
    try:
        cli('storage', 'create', POOL, 'btrfs', 'size=12GiB')
        cli('config', 'trust', 'add-certificate', str(ROOT/'manager.crt'), '--name=anas-runner-image-manager')
        cli('config', 'set', 'core.https_address', '127.0.0.1:8443')
        ready = lab.call([args.provider, 'ensure', '--isolation', 'container'], env, timeout=300)
        if json.loads(ready.stdout) != {'exists': True, 'ready': True, 'restricted': True, 'quota_enforced': True}:
            raise RuntimeError('replayed Provider lease is not fully ready')
        name, flag = CASES[args.case]
        with (report/'native.jsonl').open('xb') as output:
            result = subprocess.run([args.test2json, '-t', '-p', 'github.com/anas-project/ANAS/internal/computeclient', args.tests,
                '-test.v=test2json', '-test.count=1', '-test.timeout=6m', '-test.run=^'+name+'$'],
                stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT,
                env={'PATH': env['PATH'], 'GOMAXPROCS': '1', flag: '1'}, timeout=390)
        events = [json.loads(line) for line in (report/'native.jsonl').read_text().splitlines()]
        required = {name}
        if args.case in ('image', 'user-manager-control', 'user-config-control'):
            required |= {name+'/'+v for v in ('provider-namespace-fence', 'real-runner-binary', 'one-job-interface', 'runner-config-readable', 'engine-config-owned', 'rootless-engine-api', 'rootless-user-session', 'rootless-oci-exec-limits')}
        passed = result.returncode == 0 and not any(e.get('Action') in ('fail', 'skip') for e in events) and required.issubset({e.get('Test') for e in events if e.get('Action') == 'pass'}) and any(e.get('Action') == 'pass' and not e.get('Test') for e in events)
        for e in events:
            if e.get('Action') == 'output' and any(v in e.get('Output', '') for v in ('observation=', 'control reached', 'control_stage=', 'public_unit_control_failure=', 'FAIL')):
                print(e['Output'], end='', flush=True)
        summary = {'passed': passed, 'case': args.case, 'immutable_image_admission': args.case == 'image' and passed, 'fingerprint': pin}
        (report/'summary.json').write_text(json.dumps(summary))
        print(json.dumps(summary), flush=True)
    finally:
        if cli('project', 'show', PROJECT, check=False).returncode == 0:
            for instance in json.loads(cli('list', '--project', PROJECT, '--format=json').stdout):
                if instance['name'] != 'anas-bake-job':
                    raise RuntimeError('unknown instance; replay cleanup refused')
                cli('delete', instance['name'], '--project', PROJECT, '--force')
            for certificate in json.loads(cli('config', 'trust', 'list', '--format=json').stdout):
                if certificate.get('projects') == [PROJECT]:
                    cli('config', 'trust', 'remove', certificate['fingerprint'])
            for image in json.loads(cli('image', 'list', '--project', PROJECT, '--format=json').stdout):
                if image['fingerprint'] != pin:
                    raise RuntimeError('unknown image; replay cleanup refused')
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
        print('REPLAY_DAEMON_RESOURCES_REMOVED', flush=True)
    if not passed:
        raise RuntimeError('required native replay did not pass')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for field in ('vm-id', 'provider', 'tests', 'test2json', 'report-root'):
        parser.add_argument('--'+field, required=True)
    parser.add_argument('--case', choices=tuple(CASES), default='image')
    try:
        main(parser.parse_args())
    except Exception as error:
        print(json.dumps({'passed': False, 'error_type': type(error).__name__, 'message': str(error) if type(error) is RuntimeError else 'private replay fixture failure'}), file=sys.stderr)
        sys.exit(1)
