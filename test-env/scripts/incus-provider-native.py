#!/usr/bin/env python3
"""Behavior checks inside test-incus-daemon-native.sh's private namespaces.

The only endpoint is the harness-created socket below. This is not a second
production Incus client: no guest orchestration or arbitrary API is exposed.
"""
import argparse
import base64
import http.client
import hashlib
import json
import os
from pathlib import Path
import socket
import stat
import subprocess
import sys
import time

FIXTURE = Path('/run/anas-incus-native')


def require_isolation():
    marker = FIXTURE / 'isolation.json'
    info = marker.lstat()
    if os.geteuid() != 0 or not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600 or info.st_uid != 0 or info.st_nlink != 1:
        raise RuntimeError('private root-owned isolation marker required')
    value = json.loads(marker.read_text())
    if value.get('schema') != 'anas.incus-native-isolation/v1':
        raise RuntimeError('invalid isolation marker')
    for name in ('net', 'mnt', 'pid'):
        parent = value.get('parent_' + name + 'ns')
        if type(parent) is not int or parent <= 0 or os.stat('/proc/self/ns/' + name).st_ino == parent:
            raise RuntimeError('parent host namespace is not an authorized test scope')


class UnixConnection(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(str(FIXTURE / 'state/unix.socket'))


def api(method, path, body=None):
    require_isolation()
    connection = UnixConnection('localhost', timeout=8)
    try:
        connection.request(method, path, body=None if body is None else json.dumps(body), headers={'Content-Type': 'application/json'})
        response = connection.getresponse()
        raw = response.read((4 << 20) + 1)
        if len(raw) > 4 << 20:
            raise RuntimeError('oversized test API response')
        value = json.loads(raw)
        allowed = response.status == 200 or (method == 'POST' and response.status == 201)
        if not allowed or value.get('type') != 'sync' or value.get('status_code') != 200 or value.get('error_code', 0) != 0 or value.get('error') or value.get('operation'):
            raise RuntimeError('test API rejected ' + method + ' ' + path + ' (HTTP ' + str(response.status) + ')')
        return value.get('metadata')
    finally:
        connection.close()


def emit(check):
    print(json.dumps({'check': check, 'passed': True}), flush=True)


def wait_ready():
    require_isolation()
    end = time.monotonic() + 30
    while True:
        try:
            api('GET', '/internal/ready')
            server = api('GET', '/1.0')
            if server['auth'] != 'trusted' or server['environment']['server_clustered']:
                raise RuntimeError('native daemon must be local and independently trusted')
            print(json.dumps({'check': 'daemon_ready', 'passed': True, 'version': server['environment']['server_version']}), flush=True)
            return
        except (OSError, ValueError, RuntimeError, http.client.HTTPException):
            if time.monotonic() >= end:
                raise RuntimeError('isolated daemon did not become ready') from None
            time.sleep(.2)


def inventory():
    return {kind: api('GET', '/1.0/' + kind + '?recursion=1') for kind in ('projects', 'certificates', 'profiles', 'networks')}


def cli_control():
    """Read-only comparison; never let CLI remote discovery create a pool."""
    require_isolation()
    results = []
    for name, explicit, force in (('directory_only', False, False), ('explicit_socket', True, False), ('force_local', True, True)):
        env = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': str(FIXTURE / 'client'),
               'INCUS_DIR': str(FIXTURE / 'state'), 'INCUS_CONF': str(FIXTURE / 'client')}
        if explicit:
            env['INCUS_SOCKET'] = str(FIXTURE / 'state/unix.socket')
        command = ['/usr/bin/incus'] + (['--force-local'] if force else []) + ['storage', 'list', '--format=json']
        try:
            result = subprocess.run(command, env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=6)
            valid = result.returncode == 0 and json.loads(result.stdout) == []
            results.append({'mode': name, 'exit_code': result.returncode, 'empty_pool_inventory': valid})
        except subprocess.TimeoutExpired:
            results.append({'mode': name, 'timed_out': True, 'empty_pool_inventory': False})
    print(json.dumps({'check': 'cli_socket_selection', 'observations': results}), flush=True)
    if not results[-1]['empty_pool_inventory']:
        raise RuntimeError('explicit local CLI socket did not reach the isolated daemon')
    # Re-test the formerly hanging command, with a closed stdin and a clean
    # environment. Its result is independently checked through the fixed API.
    name = 'anas-native-cli-dir'
    if api('GET', '/1.0/storage-pools'):
        raise RuntimeError('CLI creation control requires an empty lab daemon')
    try:
        result = subprocess.run(['/usr/bin/incus', '--force-local', 'storage', 'create', name, 'dir'],
                                env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=12)
        if result.returncode != 0:
            raise RuntimeError('explicit local CLI creation failed')
        pool = api('GET', '/1.0/storage-pools/' + name)
        if pool.get('driver') != 'dir' or pool.get('status') != 'Created':
            raise RuntimeError('CLI creation is not confirmed by the independent API')
        emit('cli_creation_with_clean_environment_and_closed_stdin')
    finally:
        if '/1.0/storage-pools/' + name in api('GET', '/1.0/storage-pools'):
            api('DELETE', '/1.0/storage-pools/' + name)
    if api('GET', '/1.0/storage-pools'):
        raise RuntimeError('CLI creation control left a pool behind')


def provider_checks(binary):
    require_isolation()
    binary = Path(binary)
    if not binary.is_absolute() or not binary.is_file():
        raise RuntimeError('absolute precompiled Provider binary required')
    keys = FIXTURE / 'keys'
    for name in ('manager', 'consumer', 'wrong-pin'):
        subprocess.run(['/usr/bin/openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(keys / (name + '.key')), '-out', str(keys / (name + '.crt')), '-days', '1', '-subj', '/CN=anas-native-' + name], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15)
    cert = (keys / 'manager.crt').read_text()
    der = ''.join(line for line in cert.splitlines() if not line.startswith('-----'))
    api('POST', '/1.0/certificates', {'name': 'anas-native-manager', 'type': 'client', 'certificate': der})
    api('PATCH', '/1.0', {'config': {'core.https_address': '127.0.0.1:8443'}})
    api('POST', '/1.0/storage-pools', {'name': 'anas-native-rejection', 'driver': 'dir', 'config': {}})
    try:
        pool = api('GET', '/1.0/storage-pools/anas-native-rejection')
        if pool['driver'] != 'dir' or pool['status'] != 'Created':
            raise RuntimeError('dir pool positive control is not ready')
        emit('real_dir_pool_created')
        before = inventory()
        encode = lambda path: base64.b64encode(path.read_bytes()).decode('ascii')
        env = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': str(FIXTURE / 'client'),
               'INCUS_ENDPOINT': 'https://127.0.0.1:8443', 'INCUS_SERVER_CERT_B64': encode(FIXTURE / 'state/server.crt'),
               'INCUS_ADMIN_CERT_B64': encode(keys / 'manager.crt'), 'INCUS_ADMIN_KEY_B64': encode(keys / 'manager.key'),
               'ANAS_RESOURCE_CLIENT_CERT': encode(keys / 'consumer.crt'), 'INCUS_STORAGE_POOL': 'anas-native-rejection',
               'INCUS_NETWORK_IPV6': 'false', 'ANAS_RESOURCE_CONSUMER': 'fixture', 'ANAS_RESOURCE_SANDBOX': 'anas-native-lease',
               'ANAS_RESOURCE_INSTANCE_PREFIX': 'anas-native-', 'ANAS_RESOURCE_IMAGE_ARCHITECTURE': 'amd64',
               'ANAS_RESOURCE_MAX_INSTANCES': '1', 'ANAS_RESOURCE_CPU': '1', 'ANAS_RESOURCE_MEMORY_MIB': '512',
               'ANAS_RESOURCE_DISK_GIB': '4', 'ANAS_RESOURCE_IMAGE_ALLOWLIST': 'f' * 64}

        def invoke(operation):
            result = subprocess.run([str(binary), operation, '--isolation', 'container'], env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=40)
            output = result.stdout + result.stderr
            if len(output) > 65536 or any(value.encode() in output for name, value in env.items() if name.endswith('_B64') or name == 'ANAS_RESOURCE_CLIENT_CERT'):
                raise RuntimeError('Provider diagnostic leaked credentials or exceeded its bound')
            return result

        for _ in range(2):
            result = invoke('ensure')
            if result.returncode == 0 or b'must be a Created btrfs or zfs pool' not in result.stderr:
                raise RuntimeError('Provider did not reject the real unsupported pool')
        emit('provider_dir_rejection_repeated')
        result = invoke('inspect')
        view = json.loads(result.stdout)
        if result.returncode != 0 or view != {'exists': False, 'ready': False, 'restricted': False, 'quota_enforced': False} or any(type(v) is not bool for v in view.values()):
            raise RuntimeError('Provider missing-project inspect is incorrect')
        emit('provider_inspect_missing_is_read_only')
        correct = env['INCUS_SERVER_CERT_B64']
        env['INCUS_SERVER_CERT_B64'] = encode(keys / 'wrong-pin.crt')
        result = invoke('ensure')
        if result.returncode == 0 or b'does not match the pinned certificate' not in result.stderr:
            raise RuntimeError('Provider accepted the wrong server pin')
        emit('provider_wrong_server_pin_rejected')
        env['INCUS_SERVER_CERT_B64'] = correct
        env['INCUS_STORAGE_POOL'] = 'anas-native-missing'
        result = invoke('ensure')
        if result.returncode == 0 or b'must be a Created btrfs or zfs pool' not in result.stderr:
            raise RuntimeError('Provider did not reject a missing pool')
        emit('provider_missing_pool_rejected')
        if inventory() != before:
            raise RuntimeError('rejected Provider operation changed project/network/profile/trust inventory')
        emit('rejection_preserves_all_lease_inventory')
    finally:
        api('DELETE', '/1.0/storage-pools/anas-native-rejection')
    if api('GET', '/1.0/storage-pools'):
        raise RuntimeError('native test left storage pools behind')
    emit('test_pools_removed')


def client_checks(binary, decoder, report_root):
    """Real shared client/CLI against a private restricted-project certificate."""
    require_isolation()
    for path in (binary, decoder, report_root):
        if not path or not Path(path).is_absolute():
            raise RuntimeError('absolute native client test paths required')
    before = inventory()
    names = ('anas-native-client', 'anas-native-other')
    if any(project.get('name') in names for project in before['projects']):
        raise RuntimeError('native client projects already exist')
    cert_path = FIXTURE / 'keys/consumer.crt'
    cert = cert_path.read_text()
    der = base64.b64decode(''.join(line for line in cert.splitlines() if not line.startswith('-----')))
    fingerprint = hashlib.sha256(der).hexdigest()
    if any(item.get('fingerprint') == fingerprint for item in before['certificates']):
        raise RuntimeError('native consumer certificate is already registered')
    created = []
    try:
        for name in names:
            api('POST', '/1.0/projects', {'name': name, 'config': {'features.images': 'false', 'features.profiles': 'false'}})
            created.append(name)
        api('POST', '/1.0/certificates', {'name': 'anas-native-client', 'type': 'client',
                                      'certificate': base64.b64encode(der).decode('ascii'),
                                      'restricted': True, 'projects': [names[0]]})
        server = (FIXTURE / 'state/server.crt').read_bytes()
        server_der = base64.b64decode(b''.join(line for line in server.splitlines() if not line.startswith(b'-----')))
        lease = {'Interface': 'incus_container', 'Endpoint': 'https://127.0.0.1:8443', 'Sandbox': names[0],
                 'InstancePrefix': 'anas-native-', 'Profile': 'anas-lease', 'MaxInstances': 1, 'CPU': 1,
                 'MemoryMiB': 512, 'DiskGiB': 4, 'ImageAllowlist': ['f' * 64],
                 'ServerCertFingerprint': hashlib.sha256(server_der).hexdigest(),
                 'ServerCertB64': base64.b64encode(server).decode('ascii'),
                 'ClientCertB64': base64.b64encode(cert_path.read_bytes()).decode('ascii'),
                 'ClientKeyB64': base64.b64encode((FIXTURE / 'keys/consumer.key').read_bytes()).decode('ascii')}
        # Private tmpfs only, never put this lease or credentials in reports.
        with (FIXTURE / 'client-lease.json').open('x') as output:
            json.dump(lease, output)

        def run(name, flag, report):
            package = 'github.com/anas-project/ANAS/internal/computeclient'
            path = Path(report_root) / report
            with path.open('x') as output:
                result = subprocess.run([decoder, '-t', '-p', package, binary, '-test.v=test2json',
                                         '-test.run=^' + name + '$', '-test.count=1', '-test.timeout=60s'],
                                        env={'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'GOMAXPROCS': '2', flag: '1'},
                                        stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT, timeout=75)
            if path.stat().st_size > 1 << 20:
                raise RuntimeError('native client report is oversized')
            events = [json.loads(line) for line in path.read_text().splitlines()]
            if (result.returncode != 0 or any(e.get('Action') in ('fail', 'skip') for e in events)
                    or not any(e.get('Package') == package and e.get('Test') == name and e.get('Action') == 'pass' for e in events)
                    or not any(e.get('Package') == package and not e.get('Test') and e.get('Action') == 'pass' for e in events)):
                raise RuntimeError('native client validation failed or was skipped')

        run('TestNativeIncusClientInitialization', 'ANAS_REQUIRE_INCUS_CLIENT_NATIVE', 'client-initialization.jsonl')
        emit('client_real_cli_restart_concurrency_project_scope_and_tls_pin')
        api('DELETE', '/1.0/certificates/' + fingerprint)
        run('TestNativeIncusClientRevokedCertificate', 'ANAS_REQUIRE_INCUS_CLIENT_REVOKED_NATIVE', 'client-revoked.jsonl')
        emit('client_real_cli_revoked_certificate_rejected')
    finally:
        if any(item.get('fingerprint') == fingerprint for item in api('GET', '/1.0/certificates?recursion=1')):
            api('DELETE', '/1.0/certificates/' + fingerprint)
        for name in reversed(created):
            api('DELETE', '/1.0/projects/' + name)
    if inventory() != before:
        raise RuntimeError('native client checks changed retained daemon inventory')
    emit('client_test_projects_and_certificate_removed')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument('--ready', action='store_true')
    group.add_argument('--provider')
    group.add_argument('--cli-control', action='store_true')
    group.add_argument('--client')
    parser.add_argument('--test2json')
    parser.add_argument('--report-root')
    args = parser.parse_args()
    try:
        if args.ready:
            wait_ready()
        elif args.cli_control:
            cli_control()
        elif args.client:
            client_checks(args.client, args.test2json, args.report_root)
        else:
            provider_checks(args.provider)
    except Exception as exc:
        # Do not echo unexpected daemon documents, certificate data or argv.
        error = {'passed': False, 'error_type': type(exc).__name__}
        if type(exc) is RuntimeError:
            error['message'] = str(exc)  # Only fixed harness messages, not daemon documents.
        print(json.dumps(error), file=sys.stderr)
        sys.exit(1)
