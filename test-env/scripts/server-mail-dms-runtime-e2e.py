#!/usr/bin/env python3
# TEST_CASES: MAILMOD-T-006 MAILMOD-T-007 MAILMOD-T-015 MAILMOD-T-016 MAILMOD-T-017 MAILMOD-T-019 MAILMOD-T-021 MAILMOD-T-080 MAILMOD-T-082
"""Actual fixed DMS adapter inside the explicitly isolated Docker daemon."""
import hashlib
import json
import os
import re
import stat
from pathlib import Path
import secrets
import subprocess
import time


def required(name):
    value = os.environ.get(name, '')
    if not value:
        raise RuntimeError('explicit '+name+' required')
    return value


parent = Path(required('MAIL_RUNTIME_TEST_ROOT'))
assert parent.is_absolute() and 'anas-mail-runtime-test-' in parent.name
root = parent/'dms-fixture'
assert not root.exists()
root.mkdir(mode=0o700)
for child in ['build', 'input', 'evidence', 'mail', 'state', 'logs', 'config']:
    (root/child).mkdir(mode=0o700)
docker = required('DOCKER_CMD')
docker_root = required('ANAS_TEST_DOCKER_ROOT')
assert docker_root != '/var/lib/docker' and 'anas-mail-go-test-' in docker_root
source = Path(__file__).resolve().parents[2]
combined_only = os.environ.get('MAIL_DMS_COMBINED_ONLY') == '1'
outbound_native = os.environ.get('MAIL_DMS_NATIVE_OUTBOUND') == '1'
outbound_only = os.environ.get('MAIL_DMS_NATIVE_OUTBOUND_ONLY') == '1'
intake_quota = os.environ.get('MAIL_DMS_NATIVE_INTAKE_QUOTA') == '1'
transfer_reject = os.environ.get('MAIL_DMS_TRANSFER_REJECT_TEST') == '1'
filter_probe = os.environ.get('MAIL_DMS_FILTER_PROBE') == '1'
milter_probe = os.environ.get('MAIL_DMS_MILTER_PROBE') == '1'
bound_copy_probe = os.environ.get('MAIL_DMS_BOUND_COPY_PROBE') == '1'
assert not bound_copy_probe or milter_probe
intake_normal_only = os.environ.get('MAIL_DMS_INTAKE_NORMAL_ONLY') == '1'
resend_live = os.environ.get('MAIL_NATIVE_RESEND_PUBLIC') == '1'
worker_input = os.environ.get('MAIL_NATIVE_WORKER_INPUT', '')
worker_bridge = os.environ.get('MAIL_NATIVE_WORKER_BRIDGE', '')
vps_input = os.environ.get('MAIL_NATIVE_VPS_CROSSHOST_INPUT', '')
vps_bridge = os.environ.get('MAIL_NATIVE_VPS_CROSSHOST_BRIDGE', '')
public_input = os.environ.get('MAIL_NATIVE_VPS_PUBLIC_INPUT', '')
public_bridge = os.environ.get('MAIL_NATIVE_VPS_PUBLIC_BRIDGE', '')
crosshost_skip_intake = os.environ.get('MAIL_DMS_CROSSHOST_SKIP_INTAKE') == '1'
guard_skip_intake = os.environ.get('MAIL_DMS_GUARD_SKIP_INTAKE') == '1'
public_skip_intake = os.environ.get('MAIL_DMS_PUBLIC_SKIP_INTAKE') == '1'
worker_skip_intake = os.environ.get('MAIL_DMS_WORKER_SKIP_INTAKE') == '1'
domain_skip_intake = os.environ.get('MAIL_DMS_DOMAIN_SKIP_INTAKE') == '1'
assert not outbound_only or outbound_native and not (combined_only or intake_quota or resend_live or worker_input or vps_input or public_input or domain_skip_intake)
assert bool(worker_input) == bool(worker_bridge)
assert bool(vps_input) == bool(vps_bridge) and not (worker_input and vps_input)
assert bool(public_input) == bool(public_bridge) and not (public_input and (worker_input or vps_input))
assert not crosshost_skip_intake or (vps_input and not combined_only and not transfer_reject)
assert not guard_skip_intake or (os.environ.get('MAIL_NATIVE_VPS_GUARD_TEST') == '1' and not (vps_input or worker_input or combined_only or transfer_reject or resend_live or outbound_native or intake_quota or filter_probe or intake_normal_only))
assert not public_skip_intake or (os.environ.get('MAIL_NATIVE_VPS_PUBLIC') == '1' and public_input and not (vps_input or worker_input or guard_skip_intake or combined_only or transfer_reject or resend_live or outbound_native or intake_quota or filter_probe or intake_normal_only))
assert not public_input or public_skip_intake
assert not worker_skip_intake or (worker_input and not combined_only and not transfer_reject)
assert not domain_skip_intake or (os.environ.get('MAIL_NATIVE_TEST_HLONG_DOMAIN') == '1' and not (combined_only or transfer_reject or resend_live or vps_input or worker_input))
assert not transfer_reject or not (combined_only or outbound_native or intake_quota)
assert not filter_probe or not (combined_only or outbound_native or intake_quota or transfer_reject or resend_live or worker_input or vps_input or crosshost_skip_intake or worker_skip_intake or domain_skip_intake or intake_normal_only)
assert not milter_probe or not (filter_probe or combined_only or outbound_native or intake_quota or transfer_reject or resend_live or worker_input or vps_input or public_input or crosshost_skip_intake or worker_skip_intake or domain_skip_intake or guard_skip_intake or public_skip_intake or intake_normal_only)
assert not intake_normal_only or not (combined_only or outbound_native or intake_quota or transfer_reject or resend_live or worker_input or vps_input or crosshost_skip_intake or worker_skip_intake or domain_skip_intake)
assert not resend_live or not (combined_only or outbound_native or intake_quota or transfer_reject or worker_input or vps_input)
assert not resend_live or re.fullmatch(r'[a-f0-9]{32}', os.environ.get('MAIL_NATIVE_RESEND_NONCE', ''))
base = 'ghcr.io/docker-mailserver/docker-mailserver:16.0.1'
base_id = 'sha256:d0fe7668defe157aad57ea31b1707ad1e2fb57d7a91bdf17cbdf876549946c86'
# The exact amd64 config from that pinned manifest also identifies an offline
# classic Docker archive, which does not preserve RepoDigests on docker load.
base_config_id = 'sha256:4b35cb5d547b3cb7e2de20ce5c0687f4f4bd3694665277dda319246be115b527'
suffix = secrets.token_hex(6)
name = 'anas_mail_dms_runtime_'+suffix
image = 'anas-mail-dms-runtime:'+suffix
network = 'anas_mail_dms_runtime_'+suffix


def run(args, data=None, timeout=120, check=True):
    p = subprocess.run(args, input=data, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
    if check and p.returncode:
        (root/'evidence/last-command-error.log').write_bytes(p.stdout)
        raise RuntimeError('isolated DMS command failed; private log preserved')
    return p


def d(*args, **kwargs):
    return run([docker, *args], **kwargs)


def write(name, raw, mode=0o600):
    path = root/name
    path.write_bytes(raw.encode() if isinstance(raw, str) else raw)
    path.chmod(mode)


failure = None
cleanup = []
try:
    assert d('info', '--format', '{{.DockerRootDir}}').stdout.decode().strip() == docker_root
    base_runtime_id = d('image', 'inspect', base, '--format', '{{.Id}}').stdout.decode().strip()
    base_digests = json.loads(d('image', 'inspect', base, '--format', '{{json .RepoDigests}}').stdout)
    # Classic Docker identifies the config, while the containerd image store
    # can report the manifest as Id. Accept only the fixed manifest or its
    # verified amd64 config, never an arbitrary archive with the same tag.
    if base_runtime_id not in (base_id, base_config_id) and base.split(':', 1)[0]+'@'+base_id not in (base_digests or []):
        raise RuntimeError('fixed DMS repository digest mismatch')
    binary = source/'test-artifacts/anas-mail-dms'
    test_binary = source/'test-artifacts/mail-dms.test'
    assert binary.is_file() and test_binary.is_file()
    # A successful `go build -o` can produce a library archive when pointed at
    # the package instead of dms/cmd. Reject it before assembling a container.
    for artifact in [binary, test_binary]:
        with artifact.open('rb') as executable:
            header = executable.read(20)
        assert header[:6] == b'\x7fELF\x02\x01' and header[18:20] == b'\x3e\x00', 'Linux amd64 executable artifact required'
    write('build/anas-mail-dms', binary.read_bytes(), 0o755)
    write('input/dms.test', test_binary.read_bytes(), 0o700)
    write('build/supervisor.conf', (source/'modules/mail/dms/supervisor.conf').read_bytes())
    write('build/entrypoint.sh', (source/'modules/mail/dms/entrypoint.sh').read_bytes(), 0o755)
    runtime = (source/'modules/mail/dms/Dockerfile').read_text().split('FROM '+base+'\n', 1)[1]
    runtime = runtime.replace('COPY --from=build --chmod=0755 /out/anas-mail-dms', 'COPY anas-mail-dms').replace('COPY dms/supervisor.conf', 'COPY supervisor.conf').replace('COPY --chmod=0755 dms/entrypoint.sh', 'COPY --chmod=0755 entrypoint.sh')
    write('build/Dockerfile', 'FROM '+base+'\n'+runtime)
    built = d('build', '--network', 'none', '--label', 'anas.mail-runtime-test='+str(root), '-t', image, str(root/'build'), check=False, timeout=180)
    (root/'evidence/build.log').write_bytes(built.stdout)
    if built.returncode:
        raise RuntimeError('isolated DMS runtime assembly failed')
    token, dovecot, sender = [secrets.token_hex(32) for _ in range(3)]
    cfg = {'Host': 'mail.mailtest.invalid:9443', 'Token': token,
           'CertFile': '/input/cert.pem', 'KeyFile': '/input/key.pem', 'CapacityBytes': (1 << 20) if intake_quota else (2 << 30),
           'Runtime': {'GoURL': 'https://manage.mailtest.invalid:8443', 'DovecotToken': dovecot,
                       'SenderToken': sender, 'CAFile': '/input/cert.pem'}}
    if resend_live:
        # Fixed, previously authorized finance test credential; never print it.
        fd = os.open('/home/whl/anas-mail-runtime-20261026/resend-sending-key.json', os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, 'rb') as f:
            info = os.fstat(f.fileno())
            assert stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid() and info.st_nlink == 1 and not info.st_mode & 0o077 and info.st_size <= 8192
            resend = json.load(f)
        assert resend['provider'] == 'resend' and resend['domain'] == 'mail-poc.hlong.wang' and resend['smtp_host'] == 'smtp.resend.com' and resend['smtp_port'] == 587 and resend['smtp_username'] == 'resend' and resend['api_key']
        cfg['Outbound'] = {'default': 'resendtest', 'profiles': [{'id': 'resendtest', 'provider': 'resend', 'host': 'smtp.resend.com', 'port': 587, 'tls': 'starttls', 'username': 'resend', 'password': resend['api_key']}], 'domains': {}}
    if outbound_native:
        cfg['Outbound'] = {'default': 'starttls', 'profiles': [
            {'id': mode, 'provider': 'smtp', 'host': 'mail.mailtest.invalid',
             'port': 2587 if mode == 'starttls' else 2465, 'tls': mode,
             'username': 'native-'+mode, 'password': 'synthetic-native-'+mode}
            for mode in ['starttls', 'tls']] + [{'id': 'direct', 'provider': 'direct'}],
            'domains': {'starttls.example.test': 'starttls', 'tls.example.test': 'tls', 'direct.example.test': 'direct'}}
    write('input/dms.json', json.dumps(cfg))
    write('input/native.json', json.dumps({'Token': token, 'CAFile': '/input/cert.pem', 'Outbound': outbound_native, 'OutboundConfigured': resend_live, 'IntakeQuota': intake_quota}))
    if worker_input:
        import stat
        binding, bridge = Path(worker_input), Path(worker_bridge)
        assert binding.is_absolute() and bridge.is_absolute() and binding.parent == bridge.parent
        assert binding.name == 'worker-native.json' and bridge.name == 'bridge'
        assert not binding.is_symlink() and binding.is_file() and binding.stat().st_uid == os.getuid() and binding.stat().st_mode & 0o077 == 0 and binding.stat().st_size <= 16384
        assert not bridge.is_symlink() and bridge.is_dir() and bridge.stat().st_uid == os.getuid() and bridge.stat().st_mode & 0o077 == 0
        socket = bridge/'cloud.sock'
        assert stat.S_ISSOCK(socket.lstat().st_mode) and socket.lstat().st_uid == os.getuid()
        write('input/worker-native.json', binding.read_bytes())
    if vps_input:
        binding, bridge = Path(vps_input), Path(vps_bridge)
        assert binding.is_absolute() and bridge.is_absolute() and binding.parent == bridge.parent
        assert binding.name == 'vps-crosshost.json' and bridge.name == 'bridge'
        assert not binding.is_symlink() and binding.is_file() and binding.stat().st_uid == os.getuid() and binding.stat().st_mode & 0o077 == 0 and binding.stat().st_size <= 16384
        assert not bridge.is_symlink() and bridge.is_dir() and bridge.stat().st_uid == os.getuid() and bridge.stat().st_mode & 0o077 == 0
        assert not any(bridge.iterdir()), 'cross-host bridge must be fresh'
        write('input/vps-crosshost.json', binding.read_bytes())
    if public_input:
        binding, bridge = Path(public_input), Path(public_bridge)
        assert binding.is_absolute() and bridge.is_absolute() and binding.parent == bridge.parent
        assert binding.name == 'vps-public.json' and bridge.name == 'bridge'
        assert not binding.is_symlink() and binding.is_file() and binding.stat().st_uid == os.getuid() and binding.stat().st_mode & 0o077 == 0 and binding.stat().st_size <= 16384
        assert not bridge.is_symlink() and bridge.is_dir() and bridge.stat().st_uid == os.getuid() and bridge.stat().st_mode & 0o077 == 0
        assert not any(bridge.iterdir()), 'public bridge must be fresh'
        write('input/vps-public.json', binding.read_bytes())
    run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '2',
         '-subj', '/CN=mail.mailtest.invalid', '-addext', 'subjectAltName=DNS:mail.mailtest.invalid,DNS:manage.mailtest.invalid,DNS:webmail.mailtest.invalid',
         '-keyout', str(root/'input/key.pem'), '-out', str(root/'input/cert.pem')])
    (root/'input/key.pem').chmod(0o600)
    # A fresh deployment has no unmanaged bootstrap mailbox. The fixed adapter
    # must start alongside DMS, then persist the first real configuration while
    # DMS waits for its initial account.
    settings = {
        'MAIL_DMS_CONFIG': '/input/dms.json', 'ENABLE_CLAMAV': '0', 'ENABLE_AMAVIS': '0',
        'ENABLE_SPAMASSASSIN': '0', 'ENABLE_FAIL2BAN': '0', 'ENABLE_RSPAMD': '1',
        'ENABLE_RSPAMD_REDIS': '0', 'ENABLE_OPENDKIM': '0', 'ENABLE_OPENDMARC': '0',
        'ENABLE_POLICYD_SPF': '0', 'ENABLE_QUOTAS': '1', 'ENABLE_UPDATE_CHECK': '0',
        'PERMIT_DOCKER': 'none', 'SPOOF_PROTECTION': '0', 'SSL_TYPE': 'manual',
        'SSL_CERT_PATH': '/input/cert.pem', 'SSL_KEY_PATH': '/input/key.pem',
        'ENABLE_OAUTH2': '1', 'OAUTH2_INTROSPECTION_URL': 'https://manage.mailtest.invalid:8443/internal/protocol/introspect',
        'POSTMASTER_ADDRESS': 'bootstrap@mailboxes.anas.invalid', 'LOG_LEVEL': 'warn'}
    write('input/dms.env', '\n'.join(k+'='+v for k, v in settings.items())+'\n')
    # DMS requires eth0 even with PERMIT_DOCKER=none. An internal bridge in
    # this dedicated network namespace supplies it without an external route
    # or any published host port.
    d('network', 'create', *([] if resend_live else ['--internal']), '--label', 'anas.mail-runtime-test='+str(root), network)
    args = ['run', '-d', '--name', name, '--hostname', 'mail.mailtest.invalid',
            '--add-host', 'mail.mailtest.invalid:127.0.0.1', '--add-host', 'manage.mailtest.invalid:127.0.0.1',
            '--add-host', 'webmail.mailtest.invalid:127.0.0.1',
            '--label', 'anas.mail-runtime-test='+str(root), '--network', network,
            '--network-alias', 'mail.mailtest.invalid', '--network-alias', 'manage.mailtest.invalid',
            '--network-alias', 'direct.mailtest.invalid', '--memory', '512m',
            '--env-file', str(root/'input/dms.env')]
    block=(source/'modules/mail/docker-compose.yml').read_text().split('  anas_mail_dms:\n',1)[1].split('  anas_mail_webmail:\n',1)[0]
    match=re.search(r'^    cap_add: \[([A-Z_, ]+)\]$',block,re.M)
    assert match and '    cap_drop: [ALL]' in block
    args+=['--cap-drop','ALL']
    for capability in match[1].split(','):args+=['--cap-add',capability.strip()]
    for local, target in [('mail', '/var/mail'), ('state', '/var/mail-state'), ('logs', '/var/log/mail'),
                          ('config', '/tmp/docker-mailserver'), ('input', '/input:ro')]:
        args += ['-v', str(root/local)+':'+target]
    if worker_input:
        args += ['-v', worker_bridge+':/bridge']
    if vps_input:
        args += ['-v', vps_bridge+':/bridge']
    if public_input:
        args += ['-v', public_bridge+':/bridge']
    d(*args, image)
    if public_input:
        signal = Path(public_bridge)/'fixture-mounted'
        with signal.open('x') as f:
            signal.chmod(0o600)
            f.flush()
            os.fsync(f.fileno())
        directory_fd = os.open(public_bridge, os.O_RDONLY | os.O_DIRECTORY)
        try: os.fsync(directory_fd)
        finally: os.close(directory_fd)
    adapter_probe = '''import http.client,json,ssl
cfg=json.load(open('/input/native.json'))
connection=http.client.HTTPSConnection('mail.mailtest.invalid',9443,timeout=2,context=ssl.create_default_context(cafile=cfg['CAFile']))
connection.request('GET','/v1/startup-probe',headers={'Authorization':'Bearer '+cfg['Token']})
assert connection.getresponse().status==404
connection.close()
'''
    for _ in range(120):
        if d('exec', name, 'python3', '-c', adapter_probe, check=False).returncode == 0:
            break
        if d('inspect', '--format', '{{.State.Running}}', name, check=False).stdout.decode().strip() != 'true':
            raise RuntimeError('DMS exited before readiness')
        time.sleep(1)
    else:
        raise RuntimeError('DMS empty-account adapter startup timeout')
    # Run readback first in a fresh DMS. Go's file registration order otherwise
    # runs intake first, so a startup regression would only test a warm server.
    repeat_readback = os.environ.get('MAIL_DMS_REPEAT_READBACK') == '1'
    assert not repeat_readback or (domain_skip_intake and not resend_live)
    if not combined_only and not transfer_reject:
        startup_extra = ['-e', 'MAIL_DMS_REPEAT_READBACK=1'] if repeat_readback else []
        startup = d('exec', '-e', 'MAIL_DMS_NATIVE_CONFIG=/input/native.json', *startup_extra, name,
                    '/input/dms.test', '-test.v', '-test.run', '^TestDMSNativeReadback$',
                    '-test.timeout', '180s', timeout=210, check=False)
        (root/'evidence/cold-start.log').write_bytes(startup.stdout)
        if startup.returncode:
            raise RuntimeError('DMS native cold-start readback failed')
        if repeat_readback and b'--- PASS: TestDMSNativeReadback/repeated_readback_after_startup ' not in startup.stdout:
            raise RuntimeError('DMS repeated readback did not execute')
        if repeat_readback and b'--- PASS: TestDMSNativeReadback/production_TLS_client_and_mailbox-scoped_kick/repeated_production_readback_after_reload ' not in startup.stdout:
            raise RuntimeError('DMS repeated production readback did not execute')
    if milter_probe:
        write('input/milter-observation.py', (source/'test-env/fixtures/mail-filter-probe/milter_observation.py').read_bytes(), 0o700)
        extra = []
        if bound_copy_probe:
            write('input/copy_binding.py', (source/'test-env/fixtures/mail-filter-probe/copy_binding.py').read_bytes(), 0o700)
            write('input/copy_store.py', (source/'test-env/fixtures/mail-filter-probe/copy_store.py').read_bytes(), 0o700)
            extra = ['-e', 'MAIL_DMS_BOUND_COPY_PROBE=1']
        probe = d('exec', '-e', 'MAIL_DMS_MILTER_PROBE=1', *extra, name, 'python3', '/input/milter-observation.py', timeout=240, check=False)
        (root/'evidence/milter-observation.log').write_bytes(probe.stdout)
        required = [b'OBSERVATION: '+label+b' ' for label in (b'normal', b'folded', b'discard', b'mime')]
        required += [b'PASS: original Rspamd rejected GTUBE before LMTP']
        if bound_copy_probe:
            required += [b'BOUND COPY: '+label+b' verified input, filter edits, durable copy and native processing' for label in (b'normal', b'folded', b'discard', b'mime')]
            required += [b'BOUND COPY: same-UID body/header/extra-header changes rejected; completed replay deduplicated; missing completion remains unknown']
            required += [b'EXPERIMENT COMPLETE: bound-copy normal-path stage only; security and crash matrix remain outstanding']
        else:
            required += [b'EXPERIMENT COMPLETE: Milter transcript omits the final Received header; complete-content attestation remains blocked']
        if probe.returncode or not all(item in probe.stdout for item in required):
            raise RuntimeError('isolated Milter boundary observation failed')
    elif filter_probe:
        write('input/filter-probe.py', (source/'test-env/fixtures/mail-filter-probe/experiment.py').read_bytes(), 0o700)
        probe = d('exec', name, 'python3', '/input/filter-probe.py', timeout=150, check=False)
        (root/'evidence/filter-probe.log').write_bytes(probe.stdout)
        required_observations = [b'OBSERVATION: '+name+b' ' for name in (b'normal', b'discard', b'mime', b'folded', b'same_postfix_uid_reads_queue_marker')]
        required_passes = [b'PASS: unrelated UID blocked; same Postfix UID alone cannot identify a process', b'PASS: external forged trace did not select trusted listener', b'PASS: GTUBE rejected before trusted listener']
        if probe.returncode or not all(item in probe.stdout for item in required_observations + required_passes):
            raise RuntimeError('isolated filter transformation probe failed')
        if b'OBSERVATION: same_postfix_uid_reads_queue_marker true' in probe.stdout and b'PASS: token-only test listener accepted a changed same-UID message; content binding is required' not in probe.stdout:
            raise RuntimeError('same-UID queue forgery counterexample was not observed')
        if b'OBSERVATION: same_postfix_uid_reads_queue_marker true' in probe.stdout and b'OBSERVATION: native observer accepted altered mailbox message as stored' not in probe.stdout:
            raise RuntimeError('native observer forgery counterexample was not observed')
    else:
        pattern = '^TestDMSNativeOutbound$' if outbound_only else ('^TestDMSNativeTransferredAddressRejectKeepsOldContentPrivate$' if transfer_reject else ('^TestDMSNativeIntake$/^normal_delivery_keeps_trusted_target_original_MIME_and_attachment$' if intake_normal_only else ('^$' if combined_only or crosshost_skip_intake or worker_skip_intake or domain_skip_intake or guard_skip_intake or public_skip_intake or resend_live else '^TestDMSNative(Outbound|Intake)$')))
        extra = ['-e', 'MAIL_DMS_TRANSFER_REJECT_TEST=1'] if transfer_reject else []
        result = d('exec', '-e', 'MAIL_DMS_NATIVE_CONFIG=/input/native.json', *extra, name,
                   '/input/dms.test', '-test.v', '-test.run', pattern, '-test.timeout', '360s', timeout=390, check=False)
        (root/'evidence/native.log').write_bytes(result.stdout)
        if result.returncode:
            diagnostic = '''import json,smtplib,time
out={};start=time.monotonic()
try:
 s=smtplib.SMTP('127.0.0.1',25,timeout=15)
 out['greeting_seconds']=round(time.monotonic()-start,3)
 for label,action in [('helo',lambda:s.ehlo('localhost.localdomain')),('mail',lambda:s.mail('')),('rcpt',lambda:s.rcpt('alice@example.test'))]:
  before=time.monotonic();code,message=action();out[label]={'code':code,'message':message.decode(errors='replace'),'seconds':round(time.monotonic()-before,3)}
 s.rset();s.quit()
except Exception as exc:out['error']=str(exc)
print(json.dumps(out))
'''
            probe = d('exec', '-i', name, 'python3', '-', data=diagnostic.encode(), check=False, timeout=25)
            (root/'evidence/smtp-diagnostic.json').write_bytes(probe.stdout)
            raise RuntimeError('DMS native readback tests failed')
        if intake_normal_only and b'--- PASS: TestDMSNativeIntake/normal_delivery_keeps_trusted_target_original_MIME_and_attachment ' not in result.stdout:
            raise RuntimeError('normal native intake subtest did not pass')
        if outbound_only and b'--- PASS: TestDMSNativeOutbound ' not in result.stdout:
            raise RuntimeError('native outbound subtest did not pass')
    combined = source/'test-env/scripts/server-mail-roundcube-runtime-e2e.py'
    if combined.is_file() and not transfer_reject and not filter_probe and not milter_probe and not intake_normal_only and not outbound_only:
        environment = dict(os.environ, MAIL_DMS_FIXTURE_ROOT=str(root), MAIL_DMS_FIXTURE_CONTAINER=name, MAIL_DMS_FIXTURE_NETWORK=network)
        native_timeout = 2050 if os.environ.get('MAIL_NATIVE_WORKER_RETRY_TEST') == '1' else 1580 if public_input else 700 if vps_input else 600 if os.environ.get('MAIL_NATIVE_VPS_TEST') == '1' or worker_input else 480
        native = subprocess.run(['python3', str(combined)], env=environment, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=native_timeout)
        (root/'evidence/roundcube-runner.log').write_bytes(native.stdout)
        if native.returncode:
            raise RuntimeError('combined native Roundcube fixture failed')
    if resend_live:
        # Exactly one test submission targets the fixed recipient in a fresh
        # queue. A Postfix "sent" result is provider acceptance, not inbox
        # delivery; retain unknown/deferred outcomes without resubmitting.
        # The container creates the marker as root with mode 0600. Read it
        # through the still-running isolated container, not through the host
        # filesystem as the unprivileged controller.
        marker = d('exec', name, 'cat', '/var/mail-state/anas-resend-test-issued', timeout=15).stdout.decode().strip()
        assert marker == os.environ['MAIL_NATIVE_RESEND_NONCE']
        deadline=time.monotonic()+100
        outcome='unknown'
        while time.monotonic()<deadline:
            logs=d('logs','--tail','1000',name,check=False,timeout=15).stdout.decode(errors='replace')
            lines=[line for line in logs.splitlines() if 'to=<whlsxl@126.com>' in line and 'relay=smtp.resend.com[' in line]
            if any('status=sent' in line for line in lines):
                outcome='provider-accepted'; break
            if any('status=bounced' in line or 'status=deferred' in line for line in lines):
                outcome='failed-or-deferred'; break
            time.sleep(2)
        write('evidence/resend-result.json', json.dumps({'nonce':os.environ['MAIL_NATIVE_RESEND_NONCE'], 'postfix_status':outcome, 'external_inbox_confirmed':False}, indent=2))
        if outcome != 'provider-accepted': raise RuntimeError('Resend result not confirmed; do not resend without reconciliation')
    summary = {'passed': True, 'native_readback_executed': not (combined_only or transfer_reject),
               'native_intake_executed': not (combined_only or crosshost_skip_intake or worker_skip_intake or domain_skip_intake or guard_skip_intake or public_skip_intake or transfer_reject or resend_live or filter_probe or milter_probe or outbound_only),
               'native_outbound_enabled': outbound_native, 'native_intake_quota_enabled': intake_quota,
               'native_transfer_reject_executed': transfer_reject,
               'combined_roundcube_executed': combined.is_file() and not (transfer_reject or filter_probe or milter_probe or intake_normal_only or outbound_only),
               'base_image_id': base_runtime_id, 'base_repository_digest': base_id,
               'milter_observation_only': milter_probe, 'bound_copy_experiment': bound_copy_probe,
               'runtime_image_id': d('image', 'inspect', image, '--format', '{{.Id}}').stdout.decode().strip(),
               'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
               'test_binary_sha256': hashlib.sha256(test_binary.read_bytes()).hexdigest()}
    write('evidence/results.json', json.dumps(summary, indent=2)+'\n')
except BaseException as exc:
    failure = type(exc).__name__ + ': ' + str(exc)
finally:
    owned = d('inspect', '--format', '{{index .Config.Labels "anas.mail-runtime-test"}}', name, check=False)
    if owned.returncode == 0:
        if owned.stdout.decode().strip() != str(root):
            cleanup.append('DMS container ownership mismatch')
        else:
            write('evidence/dms.log', d('logs', name, check=False).stdout)
            write('evidence/container-state.json', d('inspect', '--format', '{{json .State}}', name, check=False).stdout)
            write('evidence/supervisor.log', d('exec', name, 'supervisorctl', 'status', check=False).stdout)
            if d('rm', '-f', '-v', name, check=False).returncode:
                cleanup.append('DMS test container removal failed')
    owned_network = d('network', 'inspect', '--format', '{{index .Labels "anas.mail-runtime-test"}}', network, check=False)
    if owned_network.returncode == 0:
        if owned_network.stdout.decode().strip() != str(root):
            cleanup.append('DMS network ownership mismatch')
        elif d('network', 'rm', network, check=False).returncode:
            cleanup.append('DMS test network removal failed')
    # Generated input and config copies contain scoped test service credentials.
    for filename in ['input/dms.json', 'input/native.json', 'input/dms.env', 'input/key.pem', 'input/worker-native.json', 'input/vps-crosshost.json', 'input/vps-public.json', 'config/dovecot.cf']:
        (root/filename).unlink(missing_ok=True)
    final = {'failure': failure, 'cleanup_errors': cleanup, 'runtime_credentials_removed':
             all(not (root/name).exists() for name in ['input/dms.json', 'input/native.json', 'input/key.pem', 'input/worker-native.json', 'input/vps-crosshost.json', 'input/vps-public.json', 'config/dovecot.cf'])}
    write('evidence/final.json', json.dumps(final, indent=2)+'\n')
    print(json.dumps(final))
if failure is not None or cleanup:
    raise SystemExit(1)
