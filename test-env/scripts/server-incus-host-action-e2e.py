#!/usr/bin/env python3
"""Installed anasd -> shared jobs -> systemd hostd acceptance in a fresh VM.

This is NOT a production installer and does not create a signed release. The
caller supplies source-bound, explicitly native-test-versioned binaries. Only
an independently identified, empty disposable QEMU/cloud-init VM is accepted.
All credentials remain in memory or in that VM's root-private experiment tree.
Neither a mock HTTP handler nor an alternate hostd backend is used.
"""
import argparse
import collections
from datetime import datetime, timedelta
import hashlib
import http.client
from http.cookies import SimpleCookie
import io
import json
import os
from pathlib import Path
import platform
import re
import secrets
import shutil
import socket
import ssl
import stat
import subprocess
import sys
import tarfile
import time
import urllib.parse

ROOT = Path('/opt/anas-host-action-e2e')
INPUTS = Path('/opt/anas-host-action-inputs')
CONFIG = Path('/etc/anas/anasd.yml')
TLS = Path('/etc/anas/host-action-fixture-tls')
ORIGIN = 'https://anas.native.test:18445'
DOCKER_ROOT = '/var/lib/anas-host-provision-test'
REQUIRED = (
    'environment_guard', 'installed_release_identity', 'https_owner_enrollment',
    'unauthenticated_request_denied', 'installed_preflight', 'typed_request_denied',
    'cross_workspace_confirmation_denied', 'cross_workspace_apply_denied',
    'confirmed_skip', 'confirmation_replay_denied',
    'confirmed_install', 'confirmed_configure', 'confirmed_enroll', 'confirmed_uninstall',
    'consumer_control_bridge_mtls', 'consumer_wrong_pin_denied',
    'consumer_without_certificate_untrusted', 'consumer_other_network_blocked',
    'service_restart_persists_consumption', 'shared_jobs_and_exit_evidence',
    'real_expiry_rejects_old_confirmation', 'fresh_plan_after_expiry',
    'confirmed_owned_package_removal', 'repeat_package_uninstall',
    'docker_baseline_restored',
)
ARTIFACTS = {
    'anas', 'anasd', 'anas-hostd', 'anas-incus-control-relay',
    'anasd.service', 'anas-hostd.socket', 'anas-hostd@.service',
    'anas-incus-control-relay.service',
}


class GateFailure(RuntimeError):
    def __init__(self, code, private_detail=None):
        self.code = code if re.fullmatch(r'[a-z_]{1,64}', code) else 'unexpected_failure'
        super().__init__(self.code)  # Never retain/format a private response.


def require(condition, code):
    if not condition:
        raise GateFailure(code)


def public_failure(error):
    return {'code': error.code if isinstance(error, GateFailure) else 'unexpected_failure'}


def public_effect_summary(state):
    steps = {'install': {'packages', 'service'},
             'configure': {'https', 'storage', 'docker-network', 'firewall', 'relay'},
             'enroll': {'trust', 'bundle'},
             'uninstall': {'bundle', 'trust', 'relay', 'firewall', 'docker-network', 'storage', 'packages'}}
    require(isinstance(state, dict) and state.get('schema') == 'anas.incus-host-state/v1' and
            isinstance(state.get('disabled', False), bool), 'effect_summary_schema')
    intents = state.get('intents') or []
    require(isinstance(intents, list) and len(intents) <= 256, 'effect_summary_bound')
    result = []
    for intent in intents:
        require(isinstance(intent, dict), 'effect_summary_schema')
        phase, step, status = (intent.get(key) for key in ('phase', 'step', 'status'))
        require(phase in steps and step in {phase + '.' + item for item in steps[phase]} and
                status in ('pending', 'ok', 'failed'), 'effect_summary_fields')
        result.append({'phase': phase, 'step': step, 'status': status})
    return {'schema': 'anas.native-effect-summary/v1', 'disabled': state.get('disabled', False), 'effects': result}


def collect_effect_summary():
    path = Path('/var/lib/anas/incus-host/state.json')
    if not path.exists():
        return
    # Read only the exact verified private file, and project a closed set of
    # step/status enums. This report can be archived without private state,
    # credentials, endpoints, arbitrary detail strings or raw daemon replies.
    before = digest_input(path)
    info = path.lstat()
    require(stat.S_IMODE(info.st_mode) == 0o600 and info.st_size <= 1 << 20, 'effect_summary_file')
    body = path.read_bytes()
    require(len(body) == info.st_size and hashlib.sha256(body).hexdigest() == before and
            digest_input(path) == before, 'effect_summary_changed')
    summary = public_effect_summary(json.loads(body))
    write_new(ROOT/'reports/effect-summary.json', json.dumps(summary, sort_keys=True))


def validate_environment(identity, facts):
    require(bool(re.fullmatch(r'anas-incus-host-[a-z0-9]{6}', identity)) and
            facts.get('uid') == 0 and facts.get('vendor') == 'QEMU' and
            facts.get('instance') == identity and facts.get('kernel') == 'Linux' and
            facts.get('docker_root') == DOCKER_ROOT and facts.get('containers') == [],
            'environment_guard')


def validate_lab_release(release):
    require(isinstance(release, dict) and
            bool(re.fullmatch(r'0\.0\.0-native\.[0-9]+(?:\.[0-9]+)*', release.get('version', ''))) and
            bool(re.fullmatch(r'[0-9a-f]{40}', release.get('commit', ''))), 'lab_release_identity')


def installation_policy_bytes(release):
    validate_lab_release(release)
    # Match the compiled struct and install.sh, including the nested release
    # order. A sorted source manifest is not the installation wire format.
    policy = {'schema': 'anas.host-action-installation/v2',
              'release': {'version': release['version'], 'commit': release['commit']},
              'service_mode': 'systemd-root-service', 'service_unit': 'anasd.service', 'socket_gid': 0}
    return (json.dumps(policy, separators=(',', ':')) + '\n').encode()


def passed(events, baseline_unchanged):
    return (baseline_unchanged is True and len(events) == len(REQUIRED) and
            collections.Counter(e.get('stage') for e in events) == collections.Counter(REQUIRED) and
            all(e.get('status') == 'passed' for e in events))


def check_public_response(response, credentials):
    body = json.dumps(response, sort_keys=True)
    require('PRIVATE KEY-----' not in body and not any(value and value in body for value in credentials),
            'private_material_in_public_response')


def parse_installed_packages(body):
    """Full dpkg inventory: half-configured and pending triggers are failures.

    Normalize only architecture suffixes, rejecting ambiguous duplicate base
    names. This experiment never uses an incomplete/errored table as absence.
    """
    require(isinstance(body, bytes) and 0 < len(body) <= 2 << 20 and body.endswith(b'\n'),
            'package_inventory_shape')
    installed, seen = set(), set()
    for line in body.decode('utf-8', errors='strict').splitlines():
        fields = line.split('\t')
        require(len(fields) == 3 and re.fullmatch(r'[a-z0-9][a-z0-9+.-]*(?::[a-z0-9-]+)?', fields[0]),
                'package_inventory_shape')
        name = fields[0].split(':')[0]
        require(name not in seen and fields[2] == 'ok' and
                fields[1] in ('installed', 'config-files', 'not-installed'), 'package_inventory_unhealthy')
        seen.add(name)
        if fields[1] == 'installed':
            installed.add(name)
    return installed


def installed_packages():
    return parse_installed_packages(capture([
        '/usr/bin/dpkg-query', '-W', '-f=${binary:Package}\\t${db:Status-Status}\\t${db:Status-Eflag}\\n']))


def validate_removed_packages(original, installed, remaining, managed):
    allowed = {'incus', 'incus-base', 'incus-client', 'btrfs-progs', 'nftables', 'dnsmasq-base'}
    require(all(isinstance(value, set) for value in (original, installed, remaining)) and
            isinstance(managed, list) and all(isinstance(name, str) for name in managed) and
            bool(managed) and len(managed) == len(set(managed)) and set(managed) <= allowed,
            'package_removal_ownership')
    owned = set(managed)
    require(original <= installed and not original & owned and owned <= installed and
            remaining == installed - owned, 'package_removal_readback')


def recorded_package_ownership():
    path = Path('/var/lib/anas/incus-host/state.json')
    digest = digest_input(path)
    require(stat.S_IMODE(path.lstat().st_mode) == 0o600 and path.stat().st_size <= 1 << 20,
            'package_ownership_file')
    body = path.read_bytes()
    require(hashlib.sha256(body).hexdigest() == digest and digest_input(path) == digest,
            'package_ownership_changed')
    state = json.loads(body)
    require(state.get('schema') == 'anas.incus-host-state/v1', 'package_ownership_schema')
    ownership = state['ownership']
    require(ownership.get('packages_installed_by_anas') is True and
            ownership.get('incus_service_by_anas') is True and not ownership.get('external_daemon_preserved'),
            'package_ownership_unverified')
    return ownership.get('managed_packages')


def require_confirmation_rejection(status, response, *, replay):
    # Match the product's actual public contract. A denial for an unrelated
    # reason (expired token, lost service, authentication failure) is NOT proof
    # of workspace binding or one-use consumption.
    expected = (409, 'confirmation_consumed') if replay else (400, 'invalid_json')
    require(isinstance(response, dict) and (status, response.get('code')) == expected and
            not any(key in response for key in ('token', 'binding_digest', 'job')),
            'confirmation_rejection_mismatch')


def require_expired_rejection(status, response):
    require(isinstance(response, dict) and status == 409 and response.get('code') == 'confirmation_expired' and
            not any(key in response for key in ('token', 'binding_digest', 'job')), 'expiry_rejection_mismatch')


def confirmation_expiry_target(created, expires):
    try:
        start = datetime.fromisoformat(created.replace('Z', '+00:00'))
        end = datetime.fromisoformat(expires.replace('Z', '+00:00'))
    except (ValueError, TypeError, AttributeError):
        raise GateFailure('confirmation_expiry_contract') from None
    require(start.tzinfo is not None and end.tzinfo is not None and end - start == timedelta(minutes=5),
            'confirmation_expiry_contract')
    return end.timestamp()


def require_live_confirmation_plan(created):
    try:
        start = datetime.fromisoformat(created.replace('Z', '+00:00'))
    except (ValueError, TypeError, AttributeError):
        raise GateFailure('confirmation_expiry_contract') from None
    require(start.tzinfo is not None and 0 <= time.time() - start.timestamp() < 300,
            'consumed_proof_plan_expired')


def decode_http_response(body, content_type):
    if not body:
        return {}
    media_type = content_type.split(';', 1)[0].strip().lower()
    require(media_type in ('application/json', 'application/problem+json'), 'response_media_type')
    try:
        decoded = json.loads(body)
    except (ValueError, UnicodeError):
        raise GateFailure('response_json') from None
    require(isinstance(decoded, dict), 'response_json_shape')
    return decoded


def capture(args, *, body=None, timeout=30):
    try:
        result = subprocess.run([str(a) for a in args], input=body, capture_output=True,
                                timeout=timeout, check=False)
    except subprocess.TimeoutExpired:
        raise GateFailure('command_timeout') from None
    require(len(result.stdout) <= 2 << 20 and len(result.stderr) <= 2 << 20, 'command_output_bound')
    require(result.returncode == 0, 'command_failed')
    return result.stdout


class UnixHTTP(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(5)
        self.sock.connect('/run/docker.sock')


def docker_get(path):
    connection = UnixHTTP('localhost', timeout=5)
    try:
        connection.request('GET', '/v1.44/' + path)
        response = connection.getresponse()
        require(response.status == 200, 'docker_read_failed')
        body = response.read((2 << 20) + 1)
        require(len(body) <= 2 << 20, 'docker_response_bound')
        return json.loads(body)
    finally:
        connection.close()


def docker_identity():
    info = docker_get('info')
    require(info.get('DockerRootDir') == DOCKER_ROOT, 'docker_identity_changed')
    return {'id': info['ID'], 'root': info['DockerRootDir'],
            'containers': sorted(row['Id'] for row in docker_get('containers/json?all=1')),
            'networks': sorted(row['Id'] for row in docker_get('networks')),
            'images': sorted(row['Id'] for row in docker_get('images/json?all=1')),
            'volumes': sorted(row['Name'] for row in (docker_get('volumes').get('Volumes') or []))}


def guarded_vm(identity):
    facts = {'uid': os.geteuid(), 'kernel': platform.system(),
             'instance': Path('/var/lib/cloud/data/instance-id').read_text().strip(),
             'vendor': Path('/sys/devices/virtual/dmi/id/sys_vendor').read_text().strip()}
    # Do not touch even the test Docker endpoint before checking VM identity.
    validate_environment(identity, {**facts, 'docker_root': DOCKER_ROOT, 'containers': []})
    baseline = docker_identity()
    validate_environment(identity, {**facts, 'docker_root': baseline['root'],
                                     'containers': baseline['containers']})
    return baseline


def digest_input(path, executable=False):
    require(path.is_absolute() and path.resolve() == path, 'input_path')
    for parent in path.parents:
        info = parent.lstat()
        require(stat.S_ISDIR(info.st_mode) and info.st_uid == 0 and not info.st_mode & 0o022,
                'input_ancestor')
    info = path.lstat()
    require(stat.S_ISREG(info.st_mode) and info.st_uid == 0 and info.st_nlink == 1 and
            not info.st_mode & 0o022 and 0 < info.st_size <= 96 << 20 and
            (not executable or info.st_mode & 0o111), 'input_identity')
    with path.open('rb') as stream:
        digest = hashlib.file_digest(stream, 'sha256').hexdigest()
        after = os.fstat(stream.fileno())
    require((info.st_dev, info.st_ino, info.st_mode, info.st_size, info.st_mtime_ns) ==
            (after.st_dev, after.st_ino, after.st_mode, after.st_size, after.st_mtime_ns),
            'input_changed')
    return digest


def write_new(path, body, mode=0o600):
    if isinstance(body, str):
        body = body.encode()
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode)
    with os.fdopen(descriptor, 'wb') as output:
        output.write(body)
        output.flush()
        os.fsync(output.fileno())


def install_fixture(manifest):
    validate_lab_release(manifest['release'])
    require(set(manifest['artifacts']) == ARTIFACTS, 'artifact_set')
    for name, digest in manifest['artifacts'].items():
        require(digest_input(INPUTS / name, not name.endswith(('.service', '.socket'))) == digest,
                'artifact_digest')
    require(set(manifest.get('test_helpers', {})) == {'incus-control-probe'}, 'probe_input_set')
    require(digest_input(INPUTS/'incus-control-probe', True) == manifest['test_helpers']['incus-control-probe'],
            'probe_input_digest')
    for path in (ROOT, CONFIG, Path('/etc/anas/hostd.json'), Path('/var/lib/anas/console'),
                 Path('/var/lib/anas/incus-host/state.json'), Path('/usr/local/bin/anasd'),
                 Path('/etc/systemd/system/anasd.service'), Path('/run/anas/hostd.sock')):
        require(not path.exists() and not path.is_symlink(), 'fresh_fixture_required')
    ROOT.mkdir(mode=0o700)
    (ROOT / 'private').mkdir(mode=0o700)
    (ROOT / 'reports').mkdir(mode=0o700)
    Path('/etc/anas').mkdir(mode=0o755, exist_ok=True)
    TLS.mkdir(mode=0o700)
    for name, destination in {'anas': '/usr/local/bin/anas', 'anasd': '/usr/local/bin/anasd',
                               'anas-hostd': '/usr/local/lib/anas/anas-hostd',
                               'anas-incus-control-relay': '/usr/local/lib/anas/anas-incus-control-relay'}.items():
        path = Path(destination)
        path.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
        if path.exists():
            require(digest_input(path, True) == manifest['artifacts'][name], 'existing_binary_not_identical')
        else:
            write_new(path, (INPUTS / name).read_bytes(), 0o755)
    for name in sorted(ARTIFACTS):
        if name.endswith(('.service', '.socket')):
            path = Path('/etc/systemd/system') / name
            if path.exists():
                require(digest_input(path) == manifest['artifacts'][name], 'existing_unit_not_identical')
            else:
                write_new(path, (INPUTS / name).read_bytes(), 0o644)
    # anasd intentionally has no version-only CLI. Its installed inode/digest
    # is checked independently below and its compiled release is compared by
    # the real bidirectional hostd handshake, not by a fixture substitute.
    require(json.loads(capture(['/usr/local/lib/anas/anas-hostd', '--version'])) == manifest['release'],
            'binary_release_mismatch')
    cli_release = json.loads(capture(['/usr/local/bin/anas', 'version', '--json']))
    require(all(cli_release.get(key) == value for key, value in manifest['release'].items()), 'cli_release_mismatch')
    for workspace in ('native', 'other'):
        directory = Path('/srv/anas') / ('host-action-' + workspace)
        (directory / '.anas').mkdir(mode=0o700, parents=True)
    hosts = Path('/etc/hosts').read_text()
    require('anas.native.test' not in hosts, 'fixture_hostname_preexists')
    with Path('/etc/hosts').open('a') as output:
        output.write('\n127.0.0.1 anas.native.test native.test\n')
    capture(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '2',
             '-subj', '/CN=ANAS disposable native test CA', '-keyout', TLS/'ca.key', '-out', TLS/'ca.crt',
             '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign'])
    capture(['openssl', 'req', '-new', '-newkey', 'rsa:2048', '-nodes', '-subj', '/CN=anas.native.test',
             '-keyout', TLS/'server.key', '-out', TLS/'server.csr'])
    write_new(TLS/'server.ext', 'basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:native.test,DNS:anas.native.test\n')
    capture(['openssl', 'x509', '-req', '-in', TLS/'server.csr', '-CA', TLS/'ca.crt', '-CAkey', TLS/'ca.key',
             '-set_serial', '1', '-days', '2', '-extfile', TLS/'server.ext', '-out', TLS/'server.crt'])
    for name in ('issuer.crt', 'trust.crt', 'internal-ca.crt'):
        write_new(TLS/name, (TLS/'ca.crt').read_bytes(), 0o644)
    write_new(TLS/'issuer-marker', 'internal\n', 0o644)
    os.chmod(TLS/'server.key', 0o600)
    os.chmod(TLS/'ca.key', 0o600)
    write_new(CONFIG, f'''api_version: anas.console-config/v1
mode: loopback
port: 18445
allowed_dns_hosts: [anas.native.test]
console_store: /var/lib/anas/console
host_actions: true
workspaces:
  - id: native
    path: /srv/anas/host-action-native
  - id: other
    path: /srv/anas/host-action-other
tls:
  lego:
    base_domain: native.test
    certificate: {TLS}/server.crt
    private_key: {TLS}/server.key
    issuer: {TLS}/issuer.crt
    trust_bundle: {TLS}/trust.crt
    internal_ca: {TLS}/internal-ca.crt
    issuer_marker: {TLS}/issuer-marker
''')
    # Canonical root installation policy, identical shape to install.sh.
    write_new(Path('/etc/anas/hostd.json'), installation_policy_bytes(manifest['release']))
    bootstrap = json.loads(capture(['/usr/local/bin/anas', 'console', 'token', '--config', CONFIG, '--json']))
    require(bootstrap.get('ok') is True and bool(bootstrap.get('token')), 'bootstrap_token')
    write_new(ROOT/'private/bootstrap.json', json.dumps(bootstrap))
    capture(['systemctl', 'daemon-reload'])
    capture(['systemctl', 'enable', '--now', 'anas-hostd.socket', 'anasd.service'])
    return bootstrap['token']


def validate_probe_container(row, identity, image, vm_id, network):
    config, host = row.get('Config') or {}, row.get('HostConfig') or {}
    attached = row.get('NetworkSettings', {}).get('Networks', {})
    state = row.get('State') or {}
    # Docker has not created an endpoint for a merely created container. Its
    # immutable HostConfig selection must already match, but empty live ID
    # is allowed only before first start. No probe pass is recorded until
    # after actual execution/exit, when the live network ID is mandatory.
    pending = state.get('Status') == 'created' and state.get('Running') is False and state.get('Pid') == 0
    ids = {v.get('NetworkID') for v in attached.values()}
    require(row.get('Id') == identity and row.get('Image') == image and
            config.get('User') == '65534:65534' and
            config.get('Labels', {}).get('dev.anas.native-control') == vm_id and
            host.get('Privileged') is False and host.get('ReadonlyRootfs') is True and
            not host.get('CapAdd') and host.get('CapDrop') == ['ALL'] and not host.get('Binds') and
            host.get('PidMode') == '' and host.get('NetworkMode') == network and
            host.get('SecurityOpt') == ['no-new-privileges:true'] and row.get('Mounts') == [] and
            len(attached) == 1 and (ids == {network} or pending and ids == {''}),
            'probe_container_identity')


def validate_probe_result(result, mode):
    require(result == {'schema': 'anas.control-probe/v1', 'mode': mode, 'passed': True}, 'probe_result')


def validate_phase_result(result, phase, *, skip):
    expected = {'install': 'disabled' if skip else 'installed', 'configure': 'configured',
                'enroll': 'connection_ready', 'uninstall': 'uninstalled'}
    require(isinstance(result, dict) and phase in expected and
            result.get('schema') == 'anas.incus-host-provision/v1' and result.get('phase') == phase and
            result.get('disposition') == expected[phase] and result.get('compute_ready') is False,
            'unexpected_phase_result')


class ControlBridgeProbe:
    """Own only a scratch image and exact temporary containers in this VM.

    No base image pull, bind mount, docker.sock mount, privileged container,
    application replacement or business Docker operation is involved.
    """
    def __init__(self, vm_id, manifest):
        self.vm_id, self.image = vm_id, None
        self.containers = {}
        self.initial = guarded_vm(vm_id)
        self.config = ROOT/'private/docker-client'
        self.config.mkdir(mode=0o700)
        require(digest_input(INPUTS/'incus-control-probe', True) == manifest['test_helpers']['incus-control-probe'],
                'probe_input_changed')
        payload = io.BytesIO()
        with tarfile.open(fileobj=payload, mode='w') as archive:
            body = (INPUTS/'incus-control-probe').read_bytes()
            entry = tarfile.TarInfo('probe'); entry.size = len(body); entry.mode = 0o555
            archive.addfile(entry, io.BytesIO(body))
        image = self.docker('import', '--change', 'USER 65534:65534',
                            '--change', 'ENTRYPOINT ["/probe"]',
                            '--change', 'LABEL dev.anas.native-control=' + vm_id,
                            '-', body=payload.getvalue()).decode().strip()
        require(bool(re.fullmatch(r'sha256:[a-f0-9]{64}', image)) and image not in self.initial['images'],
                'probe_image_identity')
        self.image = image
        self.check_image()

    def docker(self, *args, body=None, timeout=30):
        require(docker_get('info').get('ID') == self.initial['id'] and
                docker_get('info').get('DockerRootDir') == DOCKER_ROOT, 'docker_identity_changed')
        return capture(['/usr/bin/docker', '--host=unix:///run/docker.sock', '--config', self.config, *args],
                       body=body, timeout=timeout)

    def check_image(self):
        row = json.loads(self.docker('image', 'inspect', self.image))[0]
        require(row.get('Id') == self.image and row.get('Config', {}).get('Labels', {}).get('dev.anas.native-control') == self.vm_id,
                'probe_image_identity')

    def inspect(self, identity):
        row = json.loads(self.docker('container', 'inspect', identity))[0]
        validate_probe_container(row, identity, self.image, self.vm_id, self.containers[identity])
        return row

    def remove_container(self, identity):
        row = self.inspect(identity)
        # Do not use --rm: inspect actual exit state and immutable ownership
        # before removing the exact container. Cancellation never licenses
        # an unscoped kill/remove/prune operation.
        if row.get('State', {}).get('Running'):
            self.docker('container', 'stop', '--time=3', identity, timeout=10)
            row = self.inspect(identity)
        require(row.get('State', {}).get('Running') is False and row.get('State', {}).get('Pid') == 0,
                'probe_container_still_running')
        self.docker('container', 'rm', identity)
        require(identity not in {r['Id'] for r in docker_get('containers/json?all=1')}, 'probe_container_not_removed')
        del self.containers[identity]

    def execute(self, mode, network, bundle):
        require(network not in ('host', 'none') and re.fullmatch(r'[a-f0-9]{64}', network), 'probe_network')
        self.check_image()
        identity = self.docker('container', 'create', '--interactive', '--network', network,
            '--name', self.vm_id + '-probe-' + secrets.token_hex(4),
            '--label', 'dev.anas.native-control=' + self.vm_id, '--user', '65534:65534',
            '--cap-drop=ALL', '--security-opt=no-new-privileges:true', '--read-only',
            '--memory=64m', '--pids-limit=64', self.image).decode().strip()
        require(bool(re.fullmatch(r'[a-f0-9]{64}', identity)), 'probe_container_id')
        self.containers[identity] = network
        row = self.inspect(identity)
        require(row['State']['Status'] == 'created', 'probe_not_fresh')
        request = {'schema': 'anas.control-probe/v1', 'mode': mode, 'endpoint': bundle['endpoint'],
                   'server_certificate_pem': bundle['server_certificate_pem']}
        if mode != 'untrusted':
            request.update(client_certificate_pem=bundle['admin_certificate_pem'],
                           client_private_key_pem=bundle['admin_private_key_pem'])
        result = json.loads(self.docker('container', 'start', '--attach', '--interactive', identity,
                                       body=json.dumps(request).encode(), timeout=15))
        validate_probe_result(result, mode)
        row = self.inspect(identity)
        require(row['State']['Running'] is False and row['State']['ExitCode'] == 0 and not row['State']['OOMKilled'],
                'probe_exit_not_confirmed')
        self.remove_container(identity)

    def close(self):
        for identity in list(self.containers):
            self.remove_container(identity)
        if self.image is not None:
            self.check_image()
            self.docker('image', 'rm', self.image)
            self.image = None
        require(docker_identity() == self.initial, 'probe_resources_not_restored')


class Console:
    def __init__(self):
        self.context = ssl.create_default_context(cafile=str(TLS/'trust.crt'))
        self.cookies = {}
        self.csrf = ''
        self.credentials = []

    def request(self, method, path, body=None, *, headers=None, cookies=True, form=False):
        require(path.startswith('/api/v1/') and '?' not in path, 'request_path')
        fields = {'Origin': ORIGIN, 'Accept': 'application/json'}
        if self.csrf:
            fields['X-CSRF-Token'] = self.csrf
        if cookies and self.cookies:
            fields['Cookie'] = '; '.join(key + '=' + value for key, value in self.cookies.items())
        if body is not None:
            body = (urllib.parse.urlencode(body) if form else json.dumps(body)).encode()
            fields['Content-Type'] = 'application/x-www-form-urlencoded' if form else 'application/json'
        fields.update(headers or {})
        connection = http.client.HTTPSConnection('anas.native.test', 18445, context=self.context, timeout=20)
        try:
            connection.request(method, path, body=body, headers=fields)
            response = connection.getresponse()
            raw = response.read((1 << 20) + 1)
            require(len(raw) <= 1 << 20, 'response_bound')
            for key, value in response.getheaders():
                if key.lower() == 'set-cookie':
                    parsed = SimpleCookie(); parsed.load(value)
                    for name, cookie in parsed.items():
                        if cookie['max-age'] == '-1' or not cookie.value:
                            self.cookies.pop(name, None)
                        else:
                            self.cookies[name] = cookie.value
            decoded = decode_http_response(raw, response.getheader('Content-Type', ''))
            return response.status, decoded
        finally:
            connection.close()

    def expected(self, method, path, body=None, status=200, **options):
        actual, response = self.request(method, path, body, **options)
        if actual != status:
            # Only public error shape is retained. Authentication response
            # bodies and tokens are never included in diagnostics.
            code = response.get('code') if isinstance(response, dict) else None
            write_new(ROOT/'private/http-failure.json', json.dumps({'status': actual, 'expected': status,
                       'code': code if isinstance(code, str) and re.fullmatch(r'[a-z_]{1,64}', code) else None}))
            raise GateFailure('http_status')
        return response

    def ready_get(self, path):
        deadline = time.monotonic() + 30
        while True:
            try:
                return self.expected('GET', path)
            except (ConnectionRefusedError, ConnectionResetError, TimeoutError):
                # Only a read is retried. A certificate/authentication failure
                # is not startup progress, and an accepted write is never
                # resubmitted by this readiness wait.
                require(time.monotonic() < deadline, 'https_not_ready')
                time.sleep(.1)

    def enroll(self, token):
        self.csrf = self.ready_get('/api/v1/auth/csrf')['csrf_token']
        session = self.expected('POST', '/api/v1/auth/bootstrap/exchange', {'token': token})
        require(session.get('state') == 'enrollment', 'enrollment_state')
        self.csrf = session['csrf_token']
        handoff = self.expected('POST', '/api/v1/auth/enrollment/handoffs', status=201)
        require(handoff.get('target_origin') == ORIGIN, 'handoff_target')
        self.cookies.clear(); self.csrf = ''
        self.expected('POST', '/api/v1/auth/enrollment/exchange', {'handoff': handoff['handoff']},
                      status=303, cookies=False, form=True)
        self.csrf = self.cookies['__Host-anas_enrollment_csrf']
        password = secrets.token_urlsafe(36)
        owner = self.expected('POST', '/api/v1/auth/enrollment/owner', {'password': password}, status=201)
        require(owner.get('state') == 'full', 'owner_enrollment')
        self.csrf = self.expected('GET', '/api/v1/auth/csrf')['csrf_token']
        local = self.expected('POST', '/api/v1/auth/login', {'password': password})
        self.csrf = local['csrf_token']
        require(local.get('state') == 'full', 'owner_login')
        self.credentials = [token, password, handoff['handoff'], self.cookies['__Host-anas_local_session'], self.csrf]

    def envelope(self):
        return {'schema': 'anas.console-session/v1', 'origin': ORIGIN,
                'ca_pem': (TLS/'trust.crt').read_text(), 'session': {'source': 'local',
                'session_token': self.cookies['__Host-anas_local_session'], 'csrf_token': self.csrf}}

    def cli(self, *args, request=None):
        payload = self.envelope() if request is None else request
        result = json.loads(capture(['/usr/local/bin/anas', 'host', *args, '--json'],
                                    body=json.dumps(payload).encode()))
        require(result.get('ok') is True, 'cli_response')
        return result['response']

    def wait_job(self, response, timeout=2750):
        identity = response['job']['id']
        require(bool(re.fullmatch(r'[A-Za-z0-9_-]{1,128}', identity)), 'job_identity')
        deadline = time.monotonic() + timeout
        while True:
            response = self.expected('GET', '/api/v1/jobs/' + identity)
            check_public_response(response, self.credentials)
            job = response['job']
            if job['status'] not in ('queued', 'running'):
                write_new(ROOT/'reports'/('job-' + identity + '.json'), json.dumps({
                    'id': identity, 'status': job['status'], 'needs_compensation_check': job.get('needs_compensation_check'),
                    'error_code': job.get('error', {}).get('code')}))
                if job['status'] != 'succeeded' or job.get('needs_compensation_check'):
                    write_new(ROOT/'private'/('job-failure-' + identity + '.json'), json.dumps(response))
                    raise GateFailure('job_not_succeeded')
                return job
            require(time.monotonic() < deadline, 'job_timeout')
            time.sleep(.15)


def systemd_identity(manifest):
    fields = capture(['systemctl', 'show', 'anasd.service', '--property=MainPID,User,Group,ActiveState,InvocationID']).decode()
    values = dict(line.split('=', 1) for line in fields.splitlines() if '=' in line)
    require(values['ActiveState'] == 'active' and values['User'] == values['Group'] == 'root', 'service_identity')
    pid = int(values['MainPID'])
    require(pid > 1 and bool(values['InvocationID']), 'service_process')
    # Type=simple can publish its PID before execve has replaced systemd's
    # launcher. Allow only that same process/invocation to finish exec; do not
    # follow a replacement/restart or use a transient active state as success.
    deadline = time.monotonic() + 5
    while not os.path.samefile(f'/proc/{pid}/exe', '/usr/local/bin/anasd'):
        require(time.monotonic() < deadline, 'service_executable')
        time.sleep(.05)
        current = capture(['systemctl', 'show', 'anasd.service', '--property=MainPID,ActiveState,InvocationID']).decode()
        snapshot = dict(line.split('=', 1) for line in current.splitlines() if '=' in line)
        require(snapshot.get('MainPID') == str(pid) and snapshot.get('InvocationID') == values['InvocationID'] and
                snapshot.get('ActiveState') == 'active', 'service_restarted_before_identity')
    status = Path(f'/proc/{pid}/status').read_text()
    for name in ('Uid', 'Gid'):
        line = next(row for row in status.splitlines() if row.startswith(name + ':'))
        require(set(line.split()[1:]) == {'0'}, 'service_credentials')
    group_line = next(row for row in status.splitlines() if row.startswith('Groups:'))
    require(set(group_line.split()[1:]) <= {'0'}, 'service_supplementary_groups')
    require(os.path.samefile(f'/proc/{pid}/exe', '/usr/local/bin/anasd') and
            digest_input(Path('/usr/local/bin/anasd'), True) == manifest['artifacts']['anasd'], 'service_executable')
    require(Path(f'/proc/{pid}/cmdline').read_bytes().split(b'\0') ==
            [b'/usr/local/bin/anasd', b'--config', b'/etc/anas/anasd.yml', b''], 'service_command')
    sock = Path('/run/anas/hostd.sock').stat()
    require(stat.S_ISSOCK(sock.st_mode) and sock.st_uid == sock.st_gid == 0 and
            stat.S_IMODE(sock.st_mode) == 0o600, 'socket_identity')
    return {'pid': pid, 'invocation_id': values['InvocationID']}


def run(identity):
    baseline = guarded_vm(identity)
    original_packages = installed_packages()
    digest_input(INPUTS/'source-manifest.json')
    manifest = json.loads((INPUTS/'source-manifest.json').read_bytes())
    require(digest_input(Path(__file__).resolve()) == manifest['runner_sha256'], 'runner_digest')
    token = install_fixture(manifest)
    events = [{'stage': 'environment_guard', 'status': 'passed'}]
    jobs = []
    stage = 'installed_release_identity'

    def complete(name, **facts):
        record = {'stage': name, 'status': 'passed', **facts}
        events.append(record)
        print(json.dumps(record, sort_keys=True), flush=True)

    try:
        service = systemd_identity(manifest)
        complete(stage, **service)
        stage = 'https_owner_enrollment'
        client = Console(); client.enroll(token)
        complete(stage)
        stage = 'unauthenticated_request_denied'
        anonymous = Console()
        status, _ = anonymous.request('POST', '/api/v1/workspaces/native/host/actions/incus.status', {},
                                      headers={'Idempotency-Key': secrets.token_hex(16)})
        require(status in (401, 403), 'unauthenticated_action_admitted')
        complete(stage)
        stage = 'installed_preflight'
        queued = client.cli('incus-preflight', '-w', 'native', '--session-json', '-')
        job = client.wait_job(queued); jobs.append(job)
        complete(stage, job_id=job['id'])
        stage = 'typed_request_denied'
        status, _ = client.request('POST', '/api/v1/workspaces/native/host/actions/incus/install/plan',
                                    {'request': {'command': 'not-a-supported-field'}},
                                    headers={'Idempotency-Key': secrets.token_hex(16)})
        require(status == 400, 'untyped_request_admitted')
        complete(stage)

        def plan(phase, request):
            queued = client.cli('incus-plan', '-w', 'native', '--phase', phase,
                                '--request-json', json.dumps(request), '--session-json', '-')
            result = client.wait_job(queued); jobs.append(result)
            return result

        def confirmed(phase, request, name, supplied_plan=None):
            selected = supplied_plan or plan(phase, request)
            parameters = selected['result']['value']['parameters']
            confirmation = client.cli('incus-confirm', '-w', 'native', '--plan-job', selected['id'],
                                      '--action', 'incus.' + phase, '--session-json', '-')
            client.credentials.append(confirmation['token'])
            envelope = {'session': client.envelope(), 'plan_job_id': selected['id'],
                        'confirmation_token': confirmation['token'], 'parameters': parameters}
            if supplied_plan is not None:
                status, response = client.request('POST', '/api/v1/workspaces/other/host/actions/incus/install/apply',
                    {key: value for key, value in envelope.items() if key != 'session'},
                    headers={'Idempotency-Key': secrets.token_hex(16)})
                require_confirmation_rejection(status, response, replay=False)
                check_public_response(response, client.credentials)
                complete('cross_workspace_apply_denied', http_status=status)
                # The same token must remain usable in its actual workspace.
            key = secrets.token_hex(16)
            queued = client.cli('incus-apply', '-w', 'native', '--phase', phase,
                                '--request-json', '-', '--idempotency-key', key, request=envelope)
            result = client.wait_job(queued); jobs.append(result)
            value = result['result']['value']
            validate_phase_result(value, phase, skip=request.get('skip') is True)
            if name is not None:
                complete(name, job_id=result['id'], disposition=value.get('disposition'))
            return envelope, result

        stage = 'cross_workspace_confirmation_denied'
        skip = plan('install', {'skip': True})
        status, response = client.request('POST', '/api/v1/workspaces/other/host/actions/confirm',
                                   {'plan_job_id': skip['id'], 'action': 'incus.install'})
        require_confirmation_rejection(status, response, replay=False)
        check_public_response(response, client.credentials)
        complete(stage, http_status=status)
        stage = 'confirmed_skip'
        replay, _ = confirmed('install', {'skip': True}, stage, skip)

        def deny_replay():
            status, response = client.request('POST', '/api/v1/workspaces/native/host/actions/incus/install/apply',
                                        {key: value for key, value in replay.items() if key != 'session'},
                                        headers={'Idempotency-Key': secrets.token_hex(16)})
            require_confirmation_rejection(status, response, replay=True)
            check_public_response(response, client.credentials)

        stage = 'confirmation_replay_denied'; deny_replay(); complete(stage)
        # Keep this consumed-token proof independent of slow official package
        # downloads. An expired binding is correctly rejected before looking
        # up consumption; accepting that error would not prove persistence.
        stage = 'service_restart_persists_consumption'
        require_live_confirmation_plan(skip['created_at'])
        capture(['systemctl', 'restart', 'anasd.service'], timeout=30)
        newer = systemd_identity(manifest)
        require(newer['invocation_id'] != service['invocation_id'], 'service_not_restarted')
        ready = client.ready_get('/api/v1/jobs/' + jobs[-1]['id'])
        check_public_response(ready, client.credentials)
        require(ready['job']['id'] == jobs[-1]['id'] and ready['job']['status'] == 'succeeded',
                'jobs_not_persistent')
        require(systemd_identity(manifest) == newer, 'service_changed_during_readiness')
        deny_replay()
        require_live_confirmation_plan(skip['created_at'])
        fetched = client.cli('job', jobs[-1]['id'], '--session-json', '-')
        require(fetched['job']['id'] == jobs[-1]['id'] and fetched['job']['status'] == 'succeeded', 'jobs_not_persistent')
        complete(stage, **newer)
        request = {'interface': 'incus_container', 'storage_size_gib': 16}
        # Create a separate UNUSED approval now; other gates run while its
        # real five-minute deadline elapses. No clock/ledger injection or TTL
        # reduction is used in this native test.
        expiry_plan = plan('uninstall', request)
        expiring = client.cli('incus-confirm', '-w', 'native', '--plan-job', expiry_plan['id'],
                              '--action', 'incus.uninstall', '--session-json', '-')
        client.credentials.append(expiring['token'])
        expiry_wall = confirmation_expiry_target(expiry_plan['created_at'], expiring['expires_at'])
        remaining = expiry_wall - time.time()
        require(0 < remaining <= 300, 'confirmation_expiry_contract')
        expiry_monotonic = time.monotonic() + remaining + 1
        for phase in ('install', 'configure', 'enroll', 'uninstall'):
            stage = 'confirmed_' + phase
            confirmed(phase, request, stage)
            if phase == 'enroll':
                # The product has already established/trusted the connection.
                # Only test transport from fresh real Docker namespaces here;
                # this is not a substitute for Provider/project isolation.
                bundle_path = Path('/var/lib/anas/incus-host/connection.json')
                info = bundle_path.lstat()
                require(info.st_uid == 0 and stat.S_ISREG(info.st_mode) and info.st_nlink == 1 and
                        stat.S_IMODE(info.st_mode) == 0o600 and info.st_size < 65536,
                        'private_connection_identity')
                bundle = json.loads(bundle_path.read_bytes())
                control = docker_get('networks/anas-incus-control')
                outsider = docker_get('networks/bridge')
                require(control['Id'] != outsider['Id'] and control['Driver'] == outsider['Driver'] == 'bridge',
                        'probe_network_identity')
                probe = ControlBridgeProbe(identity, manifest)
                try:
                    for mode, network, label in (
                            ('trusted', control['Id'], 'consumer_control_bridge_mtls'),
                            ('pin_rejected', control['Id'], 'consumer_wrong_pin_denied'),
                            ('untrusted', control['Id'], 'consumer_without_certificate_untrusted'),
                            ('network_blocked', outsider['Id'], 'consumer_other_network_blocked')):
                        stage = label
                        probe.execute(mode, network, bundle)
                        if mode != 'trusted':
                            # A dead daemon/relay is not network/auth denial
                            # evidence. The same trusted source must remain
                            # usable immediately after each negative probe.
                            probe.execute('trusted', control['Id'], bundle)
                        complete(stage)
                finally:
                    probe.close()
                    bundle.clear()
        stage = 'real_expiry_rejects_old_confirmation'
        print(json.dumps({'stage': stage, 'status': 'waiting_for_actual_expiry'}), flush=True)
        while True:
            remaining = expiry_monotonic - time.monotonic()
            if remaining <= 0:
                break
            time.sleep(min(1, remaining))
        require(time.time() >= expiry_wall, 'confirmation_clock_changed')
        expiry_payload = {'plan_job_id': expiry_plan['id'], 'confirmation_token': expiring['token'],
                          'parameters': expiry_plan['result']['value']['parameters']}
        status, response = client.request('POST', '/api/v1/workspaces/native/host/actions/incus/uninstall/apply',
            expiry_payload, headers={'Idempotency-Key': secrets.token_hex(16)})
        require_expired_rejection(status, response)
        check_public_response(response, client.credentials)
        status, response = client.request('POST', '/api/v1/workspaces/native/host/actions/confirm',
            {'plan_job_id': expiry_plan['id'], 'action': 'incus.uninstall'})
        require_expired_rejection(status, response)
        check_public_response(response, client.credentials)
        complete(stage, plan_ttl_seconds=300)
        stage = 'fresh_plan_after_expiry'
        confirmed('uninstall', request, stage)
        stage = 'confirmed_owned_package_removal'
        managed = recorded_package_ownership()
        installed = installed_packages()
        # Check the intended deletion set against actual original inventory
        # before asking the product to confirm any destructive package action.
        validate_removed_packages(original_packages, installed, installed - set(managed), managed)
        _, removed = confirmed('uninstall', {**request, 'remove_packages': True}, None)
        remaining = installed_packages()
        validate_removed_packages(original_packages, installed, remaining, managed)
        daemon = capture(['systemctl', 'show', 'incus.service', '--property=ActiveState', '--value']).strip()
        require(daemon == b'inactive', 'owned_daemon_still_active')
        complete(stage, job_id=removed['id'], removed_package_count=len(managed), original_package_count=len(original_packages))
        stage = 'repeat_package_uninstall'
        _, repeated = confirmed('uninstall', {**request, 'remove_packages': True}, None)
        require(installed_packages() == remaining, 'repeat_uninstall_changed_inventory')
        complete(stage, job_id=repeated['id'])
        stage = 'shared_jobs_and_exit_evidence'
        # A committed successful host job has already passed its pinned PID1
        # exit observation in HostJobBroker. Independently retain the actual
        # activation identities from the system journal as native evidence.
        raw = capture(['journalctl', '--no-pager', '-o', 'json', '-u', 'anas-hostd@*'], timeout=15)
        activations = set()
        for line in raw.splitlines():
            record = json.loads(line)
            unit = record.get('UNIT') or record.get('_SYSTEMD_UNIT', '')
            if unit.startswith('anas-hostd@') and unit.endswith('.service'):
                activations.add(unit)
        require(len(activations) >= len(jobs), 'activation_evidence_incomplete')
        active = capture(['systemctl', 'list-units', '--state=active,activating,deactivating',
                          '--no-legend', '--plain', 'anas-hostd@*.service'])
        require(not active.strip(), 'host_executor_outlived_jobs')
        complete(stage, succeeded_jobs=len(jobs), observed_activations=len(activations))
        stage = 'docker_baseline_restored'
        unchanged = docker_identity() == baseline
        require(unchanged, 'docker_baseline_changed')
        complete(stage)
    except Exception as error:
        events.append({'stage': stage, 'status': 'failed', **public_failure(error)})
        unchanged = docker_identity() == baseline
        try:
            collect_effect_summary()
        except Exception as diagnostic_error:
            # Do not hide the original gate failure or print private details
            # merely because a secondary read-only diagnostic was unavailable.
            write_new(ROOT/'reports/effect-summary-error.json', json.dumps(public_failure(diagnostic_error)))
    result = {'schema': 'anas.incus-host-action-native/v1', 'vm_id': identity,
              'scope': 'installed CLI/HTTPS owner, shared jobs, systemd hostd and isolated Docker transport; not signed-release or full consumer/Compose acceptance',
              'release': manifest['release'], 'events': events,
              'passed': passed(events, unchanged), 'docker_identity_and_baseline_unchanged': unchanged}
    write_new(ROOT/'reports/summary.json', json.dumps(result, sort_keys=True, indent=2) + '\n')
    print(json.dumps(result, sort_keys=True), flush=True)
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--vm-id', required=True)
    args = parser.parse_args()
    os.umask(0o077)
    try:
        sys.exit(run(args.vm_id))
    except Exception as error:
        print(json.dumps({'passed': False, 'stage': 'fixture_preparation', **public_failure(error)}), flush=True)
        sys.exit(1)
