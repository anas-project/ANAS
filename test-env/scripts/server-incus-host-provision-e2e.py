#!/usr/bin/env python3
"""Run actual host provisioning only in an explicitly identified disposable VM.

The VM's Docker endpoint intentionally occupies the production path because the
production backend does not accept arbitrary daemon endpoints. Its identity,
dedicated data root and empty inventory are checked after exact QEMU/cloud-init
identity, never on an existing host merely because it has a Docker socket.
"""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import re
import selectors
import signal
import stat
import subprocess
import sys
import time

PARENT = 'TestNativeHostProvisionLifecycle'
REQUIRED = {PARENT} | {PARENT+'/'+name for name in (
    'confirmation_is_required', 'skip_without_host_effects', 'install_pinned_packages',
    'configure_owned_host_resources', 'enroll_private_management_connection',
    'idempotent_reenrollment', 'uninstall_preflight_preserves_retained_storage',
    'uninstall_preserves_original_packages',
    'optional_package_removal_preserves_preexisting', 'repeat_uninstall_is_idempotent')}
MARKER = Path('/run/anas-incus-host-lifecycle/identity.json')
RELAY = Path('/usr/local/lib/anas/anas-incus-control-relay')
DOCKER_ROOT = '/var/lib/anas-host-provision-test'


def require_vm(identity):
    if os.geteuid() != 0 or not re.fullmatch(r'anas-incus-host-[a-z0-9]{6}', identity):
        raise RuntimeError('explicit disposable host-provisioning VM and root are required')
    if (Path('/var/lib/cloud/data/instance-id').read_text().strip() != identity
            or Path('/sys/devices/virtual/dmi/id/sys_vendor').read_text().strip() != 'QEMU'):
        raise RuntimeError('refusing native host changes outside the exact disposable QEMU VM')


def protected_input(path):
    if not path.is_absolute() or path.resolve() != path:
        raise RuntimeError('native input must be an absolute canonical root-owned path')
    for parent in path.parents:
        info = parent.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) & 0o022:
            raise RuntimeError('native input has a writable or non-root ancestor')
    info = path.lstat()
    if (not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1
            or stat.S_IMODE(info.st_mode) & 0o022 or not stat.S_IMODE(info.st_mode) & 0o111
            or not 0 < info.st_size <= 64 << 20):
        raise RuntimeError('native executable must be bounded, regular and root-owned')
    with path.open('rb') as stream:
        body = stream.read((64 << 20)+1)
        after = os.fstat(stream.fileno())
    if (len(body) != info.st_size or (info.st_dev, info.st_ino, info.st_mode, info.st_mtime_ns)
            != (after.st_dev, after.st_ino, after.st_mode, after.st_mtime_ns)):
        raise RuntimeError('native input changed while being read')
    return hashlib.sha256(body).hexdigest()


def native_events_passed(events, exit_code):
    starts, passes = [], []
    if exit_code != 0:
        return False
    for event in events:
        if not isinstance(event, dict):
            return False
        action, name = event.get('Action'), event.get('Test')
        if action in ('fail', 'skip'):
            return False
        if name and action in ('run', 'pass'):
            if name not in REQUIRED:
                return False
            (starts if action == 'run' else passes).append(name)
    return (len(starts) == len(passes) == len(REQUIRED)
            and set(starts) == set(passes) == REQUIRED)


def stop_native_group(process):
    # The caller has not polled/reaped this child. Keep its PID reserved until
    # after both group signals so cleanup cannot target a reused numeric PGID.
    def signal_group(kind):
        try:
            os.killpg(process.pid, kind)
        except ProcessLookupError:
            pass
        except PermissionError:
            # macOS helper tests can observe EPERM for an unreaped exited
            # group. Never ignore a live child's error or weaken Linux's
            # actual native gate, which must retain all group-cleanup errors.
            if (sys.platform != 'darwin' or not hasattr(os, 'waitid') or
                    os.waitid(os.P_PID, process.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT) is None):
                raise

    signal_group(signal.SIGTERM)
    deadline = time.monotonic()+10
    if hasattr(os, 'waitid') and hasattr(os, 'WNOWAIT'):
        while time.monotonic() < deadline:
            event = os.waitid(os.P_PID, process.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
            if event is not None:
                break
            time.sleep(0.02)
    else:
        # Only local helper tests use this fallback. Native admission requires
        # Linux, which has waitid/WNOWAIT; never reap before the group signal.
        time.sleep(0.1)
    signal_group(signal.SIGKILL)
    return process.wait(timeout=10)


def run_native_process(command, env, output_path, error_path, timeout, max_log_bytes=32 << 20):
    if not math.isfinite(timeout) or timeout <= 0 or type(max_log_bytes) is not int or not 0 < max_log_bytes <= 32 << 20:
        raise ValueError('finite process deadline and bounded log budget are required')
    # Limit the two log streams in the supervisor. RLIMIT_FSIZE would also
    # truncate APT indexes, package data and Incus storage files in descendants.
    with output_path.open('xb') as output, error_path.open('xb') as errors:
        process = subprocess.Popen(command, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, start_new_session=True)
        deadline = time.monotonic()+timeout
        timed_out, log_limit_exceeded, reaped = False, False, False
        written = {process.stdout: 0, process.stderr: 0}
        try:
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ, output)
                selector.register(process.stderr, selectors.EVENT_READ, errors)
                while selector.get_map() and not log_limit_exceeded:
                    remaining = deadline-time.monotonic()
                    if remaining <= 0:
                        timed_out = True
                        break
                    for key, _ in selector.select(min(remaining, 0.2)):
                        chunk = os.read(key.fileobj.fileno(), 64 << 10)
                        if not chunk:
                            selector.unregister(key.fileobj)
                            key.fileobj.close()
                            continue
                        available = max_log_bytes-written[key.fileobj]
                        key.data.write(chunk[:available])
                        written[key.fileobj] += min(len(chunk), available)
                        if len(chunk) > available:
                            log_limit_exceeded = True
                            break
            if timed_out or log_limit_exceeded:
                code = stop_native_group(process)
                reaped = True
            else:
                # Closing stdout/stderr does not prove that the process exited.
                try:
                    code = process.wait(timeout=max(0, deadline-time.monotonic()))
                    reaped = True
                except subprocess.TimeoutExpired:
                    timed_out = True
                    code = stop_native_group(process)
                    reaped = True
        finally:
            if not reaped:
                stop_native_group(process)
            process.stdout.close()
            process.stderr.close()
    return code, timed_out, log_limit_exceeded


def main(args):
    require_vm(args.vm_id)
    tests, converter, report = Path(args.tests), Path(args.test2json), Path(args.report_root)
    identities = {'tests_sha256': protected_input(tests), 'relay_sha256': protected_input(RELAY),
                  'test2json_sha256': protected_input(converter)}
    if (not report.is_absolute() or report.exists() or report.parent.resolve() != report.parent
            or MARKER.parent.exists()):
        raise RuntimeError('fresh separate native report and marker roots are required')
    for parent in report.parents:
        info = parent.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) & 0o022:
            raise RuntimeError('native reports require protected root-owned ancestors')
    for path in ('/var/lib/anas/incus-host/state.json', '/var/lib/anas/incus-host/connection.json',
                 '/etc/anas/incus-control-relay.json', '/var/lib/incus/unix.socket'):
        if os.path.lexists(path):
            raise RuntimeError('native host provisioning fixture is not fresh')
    socket_info = Path('/run/docker.sock').lstat()
    if not stat.S_ISSOCK(socket_info.st_mode) or socket_info.st_uid != 0:
        raise RuntimeError('VM-local Docker endpoint must be a root-owned Unix socket')
    os.umask(0o077)
    report.mkdir(mode=0o700)
    (report/'home').mkdir(mode=0o700)
    (report/'docker-config').mkdir(mode=0o700)
    (report/'docker-config/config.json').write_text('{}\n')
    env = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': str(report/'home'), 'LANG': 'C.UTF-8',
           'DOCKER_CONFIG': str(report/'docker-config')}
    docker = ['/usr/bin/docker', '--host', 'unix:///run/docker.sock']

    def read_docker(arguments):
        result = subprocess.run(docker+arguments, env=env, stdin=subprocess.DEVNULL,
                                capture_output=True, timeout=30)
        if result.returncode or len(result.stdout) > 4 << 20 or len(result.stderr) > 1 << 20:
            raise RuntimeError('cannot independently inventory the VM test Docker daemon')
        return result.stdout

    daemon = json.loads(read_docker(['info', '--format', '{{json .}}']))
    if (not daemon.get('ID') or daemon.get('DockerRootDir') != DOCKER_ROOT
            or Path(DOCKER_ROOT).resolve() != Path(DOCKER_ROOT)
            or read_docker(['ps', '-aq']).strip()):
        raise RuntimeError('a dedicated test Docker data root with no existing containers is required')
    before_networks = sorted(read_docker(['network', 'ls', '--no-trunc', '--format', '{{.ID}}']).decode().split())
    marker = {'schema': 'anas.incus-host-native/v1', 'vm_id': args.vm_id, 'docker_id': daemon['ID'],
              'docker_root': DOCKER_ROOT, **identities}
    MARKER.parent.mkdir(mode=0o700)
    with MARKER.open('x') as stream:
        json.dump(marker, stream, sort_keys=True)
    (report/'environment.json').write_text(json.dumps(dict(marker, docker_version=daemon.get('ServerVersion'),
                                                         baseline_networks=before_networks), sort_keys=True)+'\n')
    env.update(ANAS_REQUIRE_INCUS_HOST_LIFECYCLE_NATIVE='1', ANAS_TEST_INCUS_HOST_VM_ID=args.vm_id)
    command = [str(converter), '-t', '-p', 'github.com/anas-project/ANAS/internal/incusprovision',
               str(tests), '-test.v=test2json', '-test.count=1', '-test.timeout=36m',
               '-test.run=^'+PARENT+'$']
    code, timed_out, log_limit_exceeded = run_native_process(
        command, env, report/'tests.jsonl', report/'tests.stderr.log', 37*60)
    events = []
    parse_complete = True
    try:
        for line in (report/'tests.jsonl').read_text().splitlines():
            events.append(json.loads(line))
    except (UnicodeError, ValueError):
        parse_complete = False
    current = json.loads(read_docker(['info', '--format', '{{json .}}']))
    after_networks = sorted(read_docker(['network', 'ls', '--no-trunc', '--format', '{{.ID}}']).decode().split())
    unchanged = (current.get('ID') == marker['docker_id'] and current.get('DockerRootDir') == DOCKER_ROOT
                 and not read_docker(['ps', '-aq']).strip() and after_networks == before_networks)
    passed = not timed_out and not log_limit_exceeded and parse_complete and native_events_passed(events, code) and unchanged
    summary = {'schema': marker['schema'], 'vm_id': args.vm_id, 'passed': passed, 'exit_code': code,
               'required_events': len(REQUIRED), 'timed_out': timed_out, 'json_complete': parse_complete,
               'log_limit_exceeded': log_limit_exceeded,
               'docker_identity_and_baseline_unchanged': unchanged,
               'passed_events': sorted(event['Test'] for event in events if isinstance(event, dict)
                                       and event.get('Action') == 'pass' and event.get('Test')),
               'failed_or_skipped': [event.get('Test', '<package>') for event in events if isinstance(event, dict)
                                     and event.get('Action') in ('fail', 'skip')],
               'scope': 'actual backend; not host-job approval, consumer reachability or compute readiness'}
    (report/'summary.json').write_text(json.dumps(summary, sort_keys=True)+'\n')
    print(json.dumps(summary, sort_keys=True), flush=True)
    # Keep protected state and marker in a failed VM for diagnosis. Never
    # expose its credential bundle or bypass pending-intent cleanup barriers.
    if not passed:
        raise RuntimeError('native host provisioning gate failed; private evidence retained')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for option in ('vm-id', 'tests', 'test2json', 'report-root'):
        parser.add_argument('--'+option, required=True)
    try:
        main(parser.parse_args())
    except Exception as error:
        print(json.dumps({'passed': False, 'error_type': type(error).__name__,
                          'message': str(error) if type(error) is RuntimeError else 'private native operation failed'}), file=sys.stderr)
        sys.exit(1)
