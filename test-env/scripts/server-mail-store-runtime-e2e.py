#!/usr/bin/env python3
# TEST_CASES: MAILMOD-T-008 MAILMOD-T-011 MAILMOD-T-012 MAILMOD-T-014
"""Real PostgreSQL tests in an explicitly isolated Docker daemon, no published port."""
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import time


def required(name):
    value = os.environ.get(name, '')
    if not value:
        raise RuntimeError('explicit '+name+' required')
    return value


root = Path(required('MAIL_RUNTIME_TEST_ROOT'))
assert root.is_absolute() and 'anas-mail-runtime-test-' in root.name and not root.exists()
docker = required('DOCKER_CMD')
docker_root = required('ANAS_TEST_DOCKER_ROOT')
assert docker_root != '/var/lib/docker' and 'anas-mail-go-test-' in docker_root
source = Path(__file__).resolve().parents[2]
binary = Path(required('MAIL_STORE_TEST_BINARY'))
image = 'ghcr.io/anas-project/anas-postgres:18.4.0-r5'
image_id = 'sha256:339c4dd98a38d35ef9ad4c9164231c9905c4cbd046287813fb8170f5f3ddd362'
# Verified linux/amd64 config under this exact OCI index. Classic Docker load
# keeps this config identity but may omit RepoDigests from an OCI archive.
image_config_id = 'sha256:9dde9de534a1962ecc0a66bb46ba8ca8727eccbe42ab846d2c7c2ffecd43c244'
name = 'anas_mail_runtime_'+secrets.token_hex(6)
root.mkdir(mode=0o700)
for child in ['input', 'data', 'evidence']:
    # PG18 drops root before revisiting PGDATA under this mount. The outer
    # fixture stays 0700; its container-visible parent must be traversable by
    # postgres, whose actual cluster is created 0700 by the fixed entrypoint.
    (root/child).mkdir(mode=0o755 if child == 'data' else 0o700)
(root/'data').chmod(0o755)


def run(args, data=None, timeout=120, check=True):
    result = subprocess.run(args, input=data, stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT, timeout=timeout)
    if check and result.returncode:
        (root/'evidence/last-command-error.log').write_bytes(result.stdout)
        raise RuntimeError('isolated command failed; private error log preserved')
    return result


def d(*args, **kwargs):
    return run([docker, *args], **kwargs)


def sql(text):
    return d('exec', '-i', name, 'sh', '-c',
             'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -U fixture_bootstrap -d postgres -v ON_ERROR_STOP=1 -At',
             data=text.encode()).stdout


def model_sql(text):
    return d('exec', '-i', name, 'sh', '-c',
             'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -U fixture_bootstrap -d anas_mail_model_1025 -v ON_ERROR_STOP=1 -At',
             data=text.encode()).stdout


def private(path, value):
    path.write_text(value)
    path.chmod(0o600)


password = secrets.token_hex(32)
role_password = secrets.token_hex(32)
private(root/'input/pg.env', 'POSTGRES_USER=fixture_bootstrap\nPOSTGRES_DB=postgres\nPOSTGRES_PASSWORD='+password+'\n')
private(root/'input/store.json', json.dumps({'dsn': 'postgres://anas_mail_model:'+role_password+'@127.0.0.1:5432/anas_mail_model_1025?sslmode=disable'}))
private(root/'input/revocation.json', json.dumps({'dsn': 'postgres://anas_mail_model:'+role_password+'@127.0.0.1:5432/anas_mail_lifecycle_1025?sslmode=disable'}))
private(root/'input/address-upgrade.json', json.dumps({'dsn': 'postgres://anas_mail_model:'+role_password+'@127.0.0.1:5432/anas_mail_cold_1025?sslmode=disable'}))
(root/'input/store.test').write_bytes(binary.read_bytes())
(root/'input/store.test').chmod(0o700)
service_binary = source/'test-artifacts/mail-service.test'
assert service_binary.is_file(), 'mail HTTP service test artifact required'
(root/'input/service.test').write_bytes(service_binary.read_bytes())
(root/'input/service.test').chmod(0o700)
ingress_binary=source/'test-artifacts/mail-ingress.test'
assert ingress_binary.is_file(), 'ingress PostgreSQL test artifact required'
(root/'input/ingress.test').write_bytes(ingress_binary.read_bytes())
(root/'input/ingress.test').chmod(0o700)
started = False
failure = None
cleanup_errors = []
try:
    assert d('info', '--format', '{{.DockerRootDir}}').stdout.decode().strip() == docker_root
    runtime_image_id = d('image', 'inspect', image, '--format', '{{.Id}}').stdout.decode().strip()
    repository_digests = json.loads(d('image', 'inspect', image, '--format', '{{json .RepoDigests}}').stdout)
    if runtime_image_id not in (image_id, image_config_id) and image.rsplit(':', 1)[0]+'@'+image_id not in (repository_digests or []):
        raise RuntimeError('fixed PostgreSQL repository digest mismatch')
    d('run', '-d', '--name', name, '--label', 'anas.mail-runtime-test='+str(root),
      '--network', 'none', '--memory', '256m', '--user', '0:0', '--env-file', str(root/'input/pg.env'),
      '-v', str(root/'data')+':/var/lib/postgresql', '-v', str(root/'input')+':/input:ro', image)
    started = True
    for _ in range(90):
        # The pinned entrypoint briefly starts a socket-only bootstrap server.
        # Wait for the final TCP server used by the actual test DSN.
        if d('exec', name, 'pg_isready', '-h', '127.0.0.1', '-U', 'fixture_bootstrap', '-d', 'postgres', check=False).returncode == 0:
            break
        if d('inspect', '--format', '{{.State.Running}}', name, check=False).stdout.decode().strip() != 'true':
            raise RuntimeError('isolated PostgreSQL exited before readiness')
        time.sleep(1)
    else:
        raise RuntimeError('isolated PostgreSQL readiness timeout')
    sql("CREATE ROLE anas_mail_model LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '"+role_password+"';\nCREATE DATABASE anas_mail_model_1025 OWNER anas_mail_model;\nCREATE DATABASE anas_mail_lifecycle_1025 OWNER anas_mail_model;\n")
    sql('CREATE DATABASE anas_mail_cold_1025 OWNER anas_mail_model;')
    audit_upgrade = d('exec', '--user', '0:0', '-e', 'MAIL_STORE_TEST_CONFIG=/input/address-upgrade.json',
                      '-e', 'MAIL_ADDRESS_AUDIT_UPGRADE=1', name, '/input/store.test', '-test.v',
                      '-test.run', '^TestPostgreSQLAddressAuditUpgradeFromV25$', '-test.timeout', '90s', check=False)
    (root/'evidence/upgrade-v25.log').write_bytes(audit_upgrade.stdout)
    if audit_upgrade.returncode:
        raise RuntimeError('v25 address audit upgrade failed')
    # Each historical fixture seeds its own old schema. Recreate only this
    # run's disposable cold database, keeping the main model/history separate.
    for version, flag, pattern in [
        (22, 'MAIL_FORWARD_UPGRADE', '^TestPostgreSQLForwardDestinationUpgradeFromV22$'),
        (24, 'MAIL_SUBMISSION_UPGRADE', '^TestPostgreSQLSubmissionUpgradeFromV24$'),
    ]:
        sql('DROP DATABASE anas_mail_cold_1025;\nCREATE DATABASE anas_mail_cold_1025 OWNER anas_mail_model;')
        result = d('exec', '--user', '0:0', '-e', 'MAIL_STORE_TEST_CONFIG=/input/address-upgrade.json',
                   '-e', flag+'=1', name, '/input/store.test', '-test.v', '-test.run', pattern,
                   '-test.timeout', '90s', timeout=105, check=False)
        (root/('evidence/upgrade-v'+str(version)+'.log')).write_bytes(result.stdout)
        if result.returncode or ('--- PASS: '+pattern.removeprefix('^').removesuffix('$')+' ') not in result.stdout.decode():
            raise RuntimeError('v'+str(version)+' upgrade failed')
    # Establish an actual v5 database before running the current migration.
    statements = ['SET ROLE anas_mail_model;', 'CREATE SCHEMA mail;', 'CREATE TABLE mail.schema_migration(version integer PRIMARY KEY,checksum text NOT NULL);']
    for number, filename in enumerate(['schema.sql', 'sessions.sql', 'login_flows.sql', 'iam_logout.sql', 'webmail_handoffs.sql'], 1):
        raw = (source/'modules/mail/store'/filename).read_bytes()
        statements.extend([raw.decode(), "INSERT INTO mail.schema_migration VALUES("+str(number)+",'"+hashlib.sha256(raw).hexdigest()+"');"])
    statements += [
        "INSERT INTO mail.person(anchor) VALUES('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa');",
        "INSERT INTO mail.mailbox(id,owner_anchor,kind,protocol_login,storage_ref,roundcube_key) VALUES('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','primary','mbx_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','store_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','webmail_cccccccccccccccccccccccccccccccc');",
        "INSERT INTO mail.address(address,mailbox_id,kind,is_default) VALUES('baseline@upgrade.example','bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb','primary',true);",
    ]
    model_sql('\n'.join(statements))
    # Independently seed the immediately previous schema with real cloud
    # history; v5 alone predates sources and cannot prove their preservation.
    legacy_files = ['schema.sql', 'sessions.sql', 'login_flows.sql', 'iam_logout.sql',
                    'webmail_handoffs.sql', 'mailbox_projection.sql', 'protocol_credentials.sql',
                    'submissions.sql', 'revocation_tasks.sql', 'submission_identity.sql',
                    'logout_flows.sql', 'directory_cursor.sql', 'company.sql',
                    'admission_groups.sql', 'runtime_start.sql', 'setup.sql', 'ingress.sql',
                    'ingress_notifications.sql', 'ingress_routes.sql', 'cloudflare_rules.sql']
    legacy = ['SET ROLE anas_mail_model;', 'CREATE SCHEMA mail;',
              'CREATE TABLE mail.schema_migration(version integer PRIMARY KEY,checksum text NOT NULL);']
    for number, filename in enumerate(legacy_files, 1):
        raw = (source/'modules/mail/store'/filename).read_bytes()
        legacy.extend([raw.decode(), "INSERT INTO mail.schema_migration VALUES("+str(number)+",'"+hashlib.sha256(raw).hexdigest()+"');"])
    legacy.append("INSERT INTO mail.ingress_route_state(source_id,generation,revision,fingerprint,sequence) VALUES('cccccccc-cccc-4ccc-8ccc-cccccccccccc','dddddddd-dddd-4ddd-8ddd-dddddddddddd',3,'"+hashlib.sha256(b'[]').hexdigest()+"',7);")
    d('exec', '-i', name, 'sh', '-c',
      'export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql -h 127.0.0.1 -U fixture_bootstrap -d anas_mail_lifecycle_1025 -v ON_ERROR_STOP=1 -At',
      data='\n'.join(legacy).encode())
    upgraded = d('exec', '--user', '0:0', '-e', 'MAIL_STORE_TEST_CONFIG=/input/revocation.json',
                 '-e', 'MAIL_INGRESS_UPGRADE=1', name, '/input/store.test', '-test.v',
                 '-test.run', '^TestPostgreSQLIngressReservationUpgradeFromNonemptyV20$',
                 '-test.timeout', '60s', timeout=75, check=False)
    (root/'evidence/upgrade-v20.log').write_bytes(upgraded.stdout)
    if upgraded.returncode:
        raise RuntimeError('nonempty source v20 upgrade failed')
    for label, extra, pattern in [
        ('upgrade-v5', ['-e', 'MAIL_STORE_TEST_UPGRADE=1', '-e', 'MAIL_STORE_TEST_FROM_VERSION=5'], '^TestPostgreSQLUpgrade$'),
        ('store', [], '^TestPostgreSQL'),
    ]:
        result = d('exec', '--user', '0:0', '-e', 'MAIL_STORE_TEST_CONFIG=/input/store.json', '-e', 'MAIL_REVOCATION_TEST_CONFIG=/input/revocation.json', *extra, name,
                   '/input/store.test', '-test.v', '-test.run', pattern, '-test.timeout', '240s', timeout=270, check=False)
        (root/('evidence/'+label+'.log')).write_bytes(result.stdout)
        if result.returncode:
            raise RuntimeError(label+' tests failed')
    log = (root/'evidence/store.log').read_text()
    def restart_phase(phase):
        result = d('exec', '--user', '0:0', '-e', 'MAIL_STORE_TEST_CONFIG=/input/store.json',
                   '-e', 'MAIL_RUNTIME_RESTART_PHASE='+phase, name, '/input/store.test',
                   '-test.v', '-test.run', '^TestPostgreSQLActualProcessRestart$',
                   '-test.timeout', '60s', timeout=75, check=False)
        (root/('evidence/restart-'+phase+'.log')).write_bytes(result.stdout)
        if result.returncode:
            raise RuntimeError('real PostgreSQL restart '+phase+' failed')
    restart_phase('before')
    d('restart', '--time', '10', name, timeout=25)
    for _ in range(20):
        if d('exec', name, 'pg_isready', '-h', '127.0.0.1', '-U', 'fixture_bootstrap', '-d', 'postgres', check=False).returncode == 0:
            break
        time.sleep(.25)
    else:
        raise RuntimeError('real PostgreSQL restart readiness timeout')
    restart_phase('after')
    service_result = d('exec', '--user', '0:0', '-e', 'MAIL_STORE_TEST_CONFIG=/input/store.json', '-e', 'MAIL_REVOCATION_TEST_CONFIG=/input/revocation.json', name,
                       '/input/service.test', '-test.v', '-test.run', '^TestPostgreSQL', '-test.timeout', '240s', timeout=270, check=False)
    (root/'evidence/http.log').write_bytes(service_result.stdout)
    if service_result.returncode:
        raise RuntimeError('HTTP PostgreSQL tests failed')
    http_log = service_result.stdout.decode()
    ingress_result=d('exec','--user','0:0','-e','MAIL_STORE_TEST_CONFIG=/input/store.json',name,
                     '/input/ingress.test','-test.v','-test.run','^TestPostgreSQL','-test.timeout','120s',timeout=150,check=False)
    (root/'evidence/ingress.log').write_bytes(ingress_result.stdout)
    if ingress_result.returncode:
        raise RuntimeError('ingress PostgreSQL/coordinator tests failed')
    summary = {'passed': True, 'image_id': runtime_image_id, 'image_repository_digest': image_id,
               'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
               'top_pass': len(re.findall(r'^--- PASS: TestPostgreSQL', log, re.M)),
               'top_skip': len(re.findall(r'^--- SKIP: TestPostgreSQL', log, re.M)),
               'v5_upgrade_passed_separately': True,
               'v20_source_upgrade_passed_separately': True,
               'v25_address_audit_upgrade_passed_separately': True,
               'v22_forward_upgrade_passed_separately': True,
               'v24_submission_upgrade_passed_separately': True,
               'real_postgres_restart_fences_old_grants': True,
               'http_binary_sha256': hashlib.sha256(service_binary.read_bytes()).hexdigest(),
               'http_top_pass': len(re.findall(r'^--- PASS: TestPostgreSQL', http_log, re.M)),
               'http_top_skip': len(re.findall(r'^--- SKIP: TestPostgreSQL', http_log, re.M)),
               'ingress_binary_sha256':hashlib.sha256(ingress_binary.read_bytes()).hexdigest(),
               'ingress_top_pass':len(re.findall(r'^--- PASS: TestPostgreSQL',ingress_result.stdout.decode(),re.M)),
               'ingress_top_skip':len(re.findall(r'^--- SKIP: TestPostgreSQL',ingress_result.stdout.decode(),re.M))}
    (root/'evidence/results.json').write_text(json.dumps(summary, indent=2)+'\n')
except BaseException as exc:
    failure = type(exc).__name__ + ': ' + str(exc)
finally:
    owned = d('inspect', '--format', '{{index .Config.Labels "anas.mail-runtime-test"}}', name, check=False)
    if owned.returncode == 0:
        if owned.stdout.decode().strip() != str(root):
            cleanup_errors.append('container ownership does not match')
        else:
            (root/'evidence/postgres.log').write_bytes(d('logs', name, check=False).stdout)
            (root/'evidence/container-state.json').write_bytes(d('inspect', '--format', '{{json .State}}', name, check=False).stdout)
            if d('rm', '-f', '-v', name, check=False).returncode:
                cleanup_errors.append('test container removal failed')
    for filename in ['pg.env', 'store.json', 'revocation.json', 'address-upgrade.json']:
        (root/'input'/filename).unlink(missing_ok=True)
    final = {'failure': failure, 'cleanup_errors': cleanup_errors,
             'runtime_credentials_removed': all(not (root/'input'/name).exists() for name in ['pg.env', 'store.json', 'revocation.json', 'address-upgrade.json'])}
    (root/'evidence/final.json').write_text(json.dumps(final, indent=2)+'\n')
    print(json.dumps(final))
if failure is not None or cleanup_errors:
    raise SystemExit(1)
