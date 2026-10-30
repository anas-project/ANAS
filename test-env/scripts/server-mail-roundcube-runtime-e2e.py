#!/usr/bin/env python3
# TEST_CASES: MAILMOD-T-021 MAILMOD-T-022 MAILMOD-T-080 MAILMOD-T-082
"""Pinned Roundcube + PG + real Go/DMS, within the parent isolated fixture."""
import hashlib
import json
import os
import re
from pathlib import Path
import secrets
import subprocess
import time

parent = Path(os.environ['MAIL_DMS_FIXTURE_ROOT'])
assert parent.is_absolute() and parent.name == 'dms-fixture'
root = parent/'roundcube-fixture'
assert not root.exists()
root.mkdir(mode=0o700)
for name in ['input', 'evidence', 'pgdata', 'temp']:
    (root/name).mkdir(mode=0o755 if name == 'pgdata' else 0o700)
# mkdir's mode is filtered by the caller's umask. PG18 revisits this mount
# after dropping root, while the enclosing fixture remains private (0700).
(root/'pgdata').chmod(0o755)
docker = os.environ['DOCKER_CMD']
dms_name = os.environ['MAIL_DMS_FIXTURE_CONTAINER']
network = os.environ['MAIL_DMS_FIXTURE_NETWORK']
source = Path(__file__).resolve().parents[2]
suffix = secrets.token_hex(6)
pg_name = 'anas_mail_native_pg_'+suffix
rc_name = 'anas_mail_native_rc_'+suffix
rc_image = 'anas-mail-webmail-runtime:'+suffix
label = 'anas.mail-runtime-test'
images = {'ghcr.io/anas-project/anas-postgres:18.4.0-r5': 'sha256:339c4dd98a38d35ef9ad4c9164231c9905c4cbd046287813fb8170f5f3ddd362',
          'roundcube/roundcubemail:1.7.5-apache': 'sha256:ebf5271ff6f15fd5939c66b31525ba1bfaefdeae8bc2b69f631e10890b1ad15f'}
# Exact amd64 configs verified against the pinned repository manifests. Classic
# Docker archive import retains these config IDs but discards RepoDigests.
image_configs = {'ghcr.io/anas-project/anas-postgres:18.4.0-r5': 'sha256:9dde9de534a1962ecc0a66bb46ba8ca8727eccbe42ab846d2c7c2ffecd43c244',
                 'roundcube/roundcubemail:1.7.5-apache': 'sha256:0fe4fa023dcd7d3d4bd0e31f331b3c052a62689681b7fc519cdb32444e0c2a4c'}


def run(args, data=None, timeout=90, check=True):
    result = subprocess.run(args, input=data, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
    if check and result.returncode:
        (root/'evidence/last-command-error.log').write_bytes(result.stdout)
        raise RuntimeError('native Roundcube fixture command failed; private log preserved')
    return result


def d(*args, **kwargs):
    return run([docker, *args], **kwargs)


def private(path, value):
    path.write_bytes(value.encode() if isinstance(value, str) else value)
    path.chmod(0o600)


failure = None
cleanup = []
try:
    for image, expected in images.items():
        runtime_id = d('image', 'inspect', image, '--format', '{{.Id}}').stdout.decode().strip()
        digests = json.loads(d('image', 'inspect', image, '--format', '{{json .RepoDigests}}').stdout)
        if runtime_id not in (expected, image_configs.get(image)) and image.rsplit(':', 1)[0]+'@'+expected not in (digests or []):
            raise RuntimeError('fixed native dependency repository digest mismatch')
    assert d('inspect', '--format', '{{index .Config.Labels "'+label+'"}}', dms_name).stdout.decode().strip() == str(parent)
    d('build', '--network', 'none', '-t', rc_image, '-f', str(source/'modules/mail/roundcube/Dockerfile'),
      str(source/'modules/mail'), timeout=120)
    # Lint as root does not detect an archive that makes image sources
    # unreadable to Apache. Check the actual web UID before cold start.
    readability = d('run', '--rm', '--network', 'none', '--user', '33:33', '--entrypoint', 'php', rc_image, '-r',
                    '$paths=["/var/roundcube/config/anas-mail.php", "/usr/src/roundcubemail/plugins/anas_mail/anas_mail.php", "/usr/src/roundcubemail/plugins/anas_mail/directory.php", "/usr/src/roundcubemail/plugins/anas_mail/localization/en_US.inc"]; foreach($paths as $p) { if(!is_readable($p)) {fwrite(STDERR,"web source unreadable\\n"); exit(1);} } echo "web sources readable\\n";')
    (root/'evidence/web-source-readability.log').write_bytes(readability.stdout)
    for path in ['plugins/anas_mail/anas_mail.php', 'config/anas-mail.php']:
        absolute = '/usr/src/roundcubemail/'+path if path.startswith('plugins') else '/var/roundcube/'+path
        lint = d('run', '--rm', '--network', 'none', '--entrypoint', 'php', rc_image, '-l', absolute)
        (root/('evidence/lint-'+Path(path).name+'.log')).write_bytes(lint.stdout)
    status_test = source/'modules/mail/roundcube/tests/status.php'
    render = d('run', '--rm', '--network', 'none', '-v', str(status_test)+':/tmp/anas-status-test.php:ro',
               '--entrypoint', 'php', rc_image, '/tmp/anas-status-test.php', '/usr/src/roundcubemail/plugins/anas_mail/anas_mail.php')
    (root/'evidence/status-render.log').write_bytes(render.stdout)
    password, role_password, rc_password, service, encryption = [secrets.token_hex(32) for _ in range(5)]
    private(root/'input/pg.env', 'POSTGRES_USER=fixture_bootstrap\nPOSTGRES_DB=postgres\nPOSTGRES_PASSWORD='+password+'\n')
    # Share only the isolated DMS network namespace: Go and test PG use loopback.
    d('run', '-d', '--name', pg_name, '--label', label+'='+str(root), '--network', 'container:'+dms_name,
      '--memory', '256m', '--user', '0:0', '--env-file', str(root/'input/pg.env'),
      '-v', str(root/'pgdata')+':/var/lib/postgresql', 'ghcr.io/anas-project/anas-postgres:18.4.0-r5')
    for _ in range(90):
        if d('exec', pg_name, 'pg_isready', '-t', '2', '-h', '127.0.0.1', '-U', 'fixture_bootstrap', check=False, timeout=10).returncode == 0:
            break
        if d('inspect', '--format', '{{.State.Running}}', pg_name).stdout.decode().strip() != 'true':
            raise RuntimeError('combined native PG exited before readiness')
        time.sleep(1)
    else:
        raise RuntimeError('combined native PG startup timeout')
    sql = "CREATE ROLE anas_mail_model LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '"+role_password+"';\nCREATE DATABASE anas_mail_cold_1025 OWNER anas_mail_model;\nCREATE ROLE anas_webmail_model LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '"+rc_password+"';\nCREATE DATABASE anas_mail_roundcube_1026 OWNER anas_webmail_model;\n"
    d('exec', '-i', pg_name, 'sh', '-c', 'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -U fixture_bootstrap -d postgres -v ON_ERROR_STOP=1', data=sql.encode())
    cfg = json.loads((parent/'input/dms.json').read_text())
    rc_dsn = 'pgsql://anas_webmail_model:'+rc_password+'@mail.mailtest.invalid:5432/anas_mail_roundcube_1026'
    rc = {'go_origin': 'https://manage.mailtest.invalid:8444', 'public_origin': 'https://webmail.mailtest.invalid:8444',
          'internal_url': 'https://manage.mailtest.invalid:8443', 'service_token': service,
          'ca_file': '/run/anas/mail-webmail-input/cert.pem', 'dms_host': 'mail.mailtest.invalid',
          'encryption_key': encryption, 'db_dsnw': rc_dsn}
    rc.update(language='en', company_directory=True)
    # Exercise the owned startup copy from private host input to www-data.
    rc_input = root/'rc-input'; rc_input.mkdir(mode=0o755)
    rc_input.chmod(0o755)
    private(rc_input/'config.json', json.dumps(rc))
    (rc_input/'cert.pem').write_bytes((parent/'input/cert.pem').read_bytes())
    (rc_input/'cert.pem').chmod(0o644)
    block=(source/'modules/mail/docker-compose.yml').read_text().split('  anas_mail_webmail:\n',1)[1]
    match=re.search(r'^    cap_add: \[([A-Z_, ]+)\]$',block,re.M)
    assert match and '    cap_drop: [ALL]' in block
    capabilities=['--cap-drop','ALL']
    for capability in match[1].split(','):capabilities+=['--cap-add',capability.strip()]
    d('run', '-d', '--name', rc_name, '--hostname', 'webmail.mailtest.invalid', '--label', label+'='+str(root),
      '--network', network, '--network-alias', 'anas-webmail-native', '--memory', '256m', *capabilities,
      '-e', 'ROUNDCUBEMAIL_DB_TYPE=pgsql', '-e', 'ROUNDCUBEMAIL_DB_HOST=mail.mailtest.invalid',
      '-e', 'ROUNDCUBEMAIL_DB_USER=anas_webmail_model', '-e', 'ROUNDCUBEMAIL_DB_NAME=anas_mail_roundcube_1026',
      '--tmpfs', '/run/anas/mail-webmail:rw,noexec,nosuid,size=1m,mode=755',
      '-v', str(rc_input)+':/run/anas/mail-webmail-input:ro', '-v', str(root/'temp')+':/tmp/roundcube-temp', rc_image)
    for _ in range(60):
        ready = d('exec', rc_name, 'php', '-r', '$s=@fsockopen("127.0.0.1",80);exit($s?0:1);', check=False)
        if ready.returncode == 0:
            break
        if d('inspect', '--format', '{{.State.Running}}', rc_name, check=False).stdout.decode().strip() != 'true':
            raise RuntimeError('native Roundcube exited before ready')
        time.sleep(1)
    else:
        raise RuntimeError('native Roundcube startup timeout')
    d('exec', pg_name, 'sh', '-c', 'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -U fixture_bootstrap -d anas_mail_roundcube_1026 -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM session; SELECT count(*) FROM users; SELECT count(*) FROM identities; SELECT count(*) FROM contacts;"')
    assert d('exec', rc_name, 'stat', '-c', '%u:%a', '/run/anas/mail-webmail/config.json').stdout.decode().strip() == '33:600'
    d('exec', rc_name, 'php', '-r', 'require "/var/www/html/program/include/iniset.php"; require "/var/www/html/plugins/anas_mail/directory.php"; $b=new anas_company_directory(null); if(!$b->readonly || !$b->groups) exit(1);')
    # Test-only inputs are mounted under /input in the DMS container already.
    private(parent/'input/roundcube-store.json', json.dumps({'dsn': 'postgres://anas_mail_model:'+role_password+'@127.0.0.1:5432/anas_mail_cold_1025?sslmode=disable'}))
    native = {'DMS': cfg['Token'], 'Dovecot': cfg['Runtime']['DovecotToken'], 'Sender': cfg['Runtime']['SenderToken'],
              'Roundcube': service, 'CAFile': '/input/cert.pem', 'CertFile': '/input/cert.pem', 'KeyFile': '/input/key.pem',
              'RoundcubeDSN': rc_dsn.replace('pgsql://', 'postgres://')+'?sslmode=disable'}
    private(parent/'input/roundcube-native.json', json.dumps(native))
    (parent/'input/service.test').write_bytes((source/'test-artifacts/mail-service.test').read_bytes())
    (parent/'input/service.test').chmod(0o700)
    vps_test = os.environ.get('MAIL_NATIVE_VPS_TEST') == '1'
    extra = ['-e', 'MAIL_NATIVE_VPS_TEST=1'] if vps_test else []
    guard_test = os.environ.get('MAIL_NATIVE_VPS_GUARD_TEST') == '1'
    public_test = bool(os.environ.get('MAIL_NATIVE_VPS_PUBLIC_INPUT'))
    assert not public_test or (os.environ.get('MAIL_NATIVE_VPS_PUBLIC') == '1' and not guard_test and not vps_test)
    if guard_test:
        extra += ['-e', 'MAIL_NATIVE_VPS_GUARD_TEST=1']
    if public_test:
        extra += ['-e', 'MAIL_NATIVE_VPS_PUBLIC_CONFIG=/input/vps-public.json']
    if os.environ.get('MAIL_NATIVE_RESEND_PUBLIC') == '1':
        nonce = os.environ.get('MAIL_NATIVE_RESEND_NONCE', '')
        assert re.fullmatch(r'[a-f0-9]{32}', nonce)
        extra += ['-e', 'MAIL_NATIVE_RESEND_PUBLIC=1', '-e', 'MAIL_NATIVE_RESEND_NONCE='+nonce]
    elif os.environ.get('MAIL_NATIVE_TEST_HLONG_DOMAIN') == '1':
        extra += ['-e', 'MAIL_NATIVE_TEST_HLONG_DOMAIN=1']
    kind = os.environ.get('MAIL_NATIVE_RESEND_KIND', '')
    if kind:
        nonce = os.environ.get('MAIL_NATIVE_RESEND_NONCE', '')
        assert kind in ('primary', 'alias', 'additional') and re.fullmatch(r'[a-f0-9]{32}', nonce)
        assert os.environ.get('MAIL_NATIVE_RESEND_PUBLIC') == '1' or os.environ.get('MAIL_NATIVE_TEST_HLONG_DOMAIN') == '1'
        extra += ['-e', 'MAIL_NATIVE_RESEND_KIND='+kind]
        if os.environ.get('MAIL_NATIVE_RESEND_PUBLIC') != '1':
            extra += ['-e', 'MAIL_NATIVE_RESEND_NONCE='+nonce]
    if vps_test and os.environ.get('MAIL_NATIVE_VPS_FORWARD_TEST') == '1':
        extra += ['-e', 'MAIL_NATIVE_VPS_FORWARD_TEST=1']
    if vps_test and os.environ.get('MAIL_NATIVE_VPS_OFFLINE_TEST') == '1':
        extra += ['-e', 'MAIL_NATIVE_VPS_OFFLINE_TEST=1']
    if vps_test and os.environ.get('MAIL_NATIVE_VPS_CROSSHOST_INPUT'):
        extra += ['-e', 'MAIL_NATIVE_VPS_CROSSHOST_CONFIG=/input/vps-crosshost.json']
    worker_test = bool(os.environ.get('MAIL_NATIVE_WORKER_INPUT'))
    if worker_test:
        extra += ['-e', 'MAIL_NATIVE_WORKER_CONFIG=/input/worker-native.json']
    crosshost = bool(os.environ.get('MAIL_NATIVE_VPS_CROSSHOST_INPUT'))
    test_selector = '^TestRoundcubeNativeMailProtocol$/^cloudflare-vps-public-and-real-native-inbox$' if public_test else '^TestRoundcubeNativeMailProtocol$/^vps-guarded-source-and-real-native-inbox$' if guard_test else '^TestRoundcubeNativeMailProtocol$'
    result = d('exec', *extra, '-e', 'MAIL_STORE_TEST_CONFIG=/input/roundcube-store.json',
               '-e', 'MAIL_ROUNDCUBE_NATIVE_CONFIG=/input/roundcube-native.json', dms_name,
               '/input/service.test', '-test.v', '-test.run', test_selector, '-test.timeout', '1950s' if os.environ.get('MAIL_NATIVE_WORKER_RETRY_TEST') == '1' else '1500s' if public_test else '600s' if crosshost else '480s' if worker_test else '420s' if vps_test else '300s', timeout=1980 if os.environ.get('MAIL_NATIVE_WORKER_RETRY_TEST') == '1' else 1530 if public_test else 630 if crosshost else 510 if worker_test else 450 if vps_test else 330, check=False)
    (root/'evidence/native.log').write_bytes(result.stdout)
    if result.returncode:
        raise RuntimeError('native Roundcube/Go/DMS protocol test failed')
    # The Go fixture has now provisioned the first mailbox and started Postfix.
    # Before that, a fresh DMS intentionally has no public SMTP listener.
    # Roundcube is a separate container on the mail network. Its ability to
    # reach public SMTP proves DNS/network are working; the cloud-only
    # listener must remain inaccessible outside the DMS network namespace.
    isolation = d('exec', rc_name, 'php', '-r', '$s=@fsockopen("mail.mailtest.invalid",25,$e,$m,3); if(!$s) exit(1); fclose($s); $s=@fsockopen("mail.mailtest.invalid",2525,$e,$m,3); if($s){fclose($s);exit(2);} echo "public SMTP reachable; cloud intake loopback private\\n";')
    (root/'evidence/private-listener.log').write_bytes(isolation.stdout)
    if guard_test and b'--- PASS: TestRoundcubeNativeMailProtocol/vps-guarded-source-and-real-native-inbox ' not in result.stdout:
        raise RuntimeError('guarded native subtest did not execute')
    if public_test and b'--- PASS: TestRoundcubeNativeMailProtocol/cloudflare-vps-public-and-real-native-inbox ' not in result.stdout:
        raise RuntimeError('public native subtest did not execute')
    (root/'evidence/results.json').write_text(json.dumps({'passed': True, 'images': images,
        'runtime_image_id': d('image', 'inspect', rc_image, '--format', '{{.Id}}').stdout.decode().strip(),
        'service_test_sha256': hashlib.sha256((source/'test-artifacts/mail-service.test').read_bytes()).hexdigest()}, indent=2)+'\n')
except BaseException as exc:
    failure = type(exc).__name__ + ': ' + str(exc)
finally:
    for name in [rc_name, pg_name]:
        owned = d('inspect', '--format', '{{index .Config.Labels "'+label+'"}}', name, check=False)
        if owned.returncode == 0:
            if owned.stdout.decode().strip() != str(root):
                cleanup.append('native container ownership mismatch')
            else:
                (root/('evidence/'+name+'.log')).write_bytes(d('logs', name, check=False).stdout)
                if d('rm', '-f', '-v', name, check=False).returncode:
                    cleanup.append('native container removal failed')
    for path in [root/'input/pg.env', root/'rc-input/config.json', parent/'input/roundcube-store.json', parent/'input/roundcube-native.json', parent/'input/worker-native.json', parent/'input/vps-crosshost.json']:
        path.unlink(missing_ok=True)
    final = {'failure': failure, 'cleanup_errors': cleanup, 'runtime_credentials_removed':
             all(not p.exists() for p in [root/'input/pg.env', root/'rc-input/config.json', parent/'input/roundcube-store.json', parent/'input/roundcube-native.json', parent/'input/worker-native.json', parent/'input/vps-crosshost.json'])}
    (root/'evidence/final.json').write_text(json.dumps(final, indent=2)+'\n')
    print(json.dumps(final))
if failure is not None or cleanup:
    raise SystemExit(1)
