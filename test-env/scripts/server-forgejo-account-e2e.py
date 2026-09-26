#!/usr/bin/env python3
"""Fixed-version Forgejo account lifecycle in an exclusively owned fresh VM.

Uses the actual product entrypoint over stdin and real Forgejo HTTP/password
verification. SQLite is an explicit isolated test backend, not evidence of a
complete Core/Compose/PostgreSQL/IAM deployment or of controller guest cleanup.
"""
import argparse
import base64
import hashlib
import http.client
import json
import os
from pathlib import Path
import pwd
import re
import secrets
import shutil
import socket
import sqlite3
import stat
import subprocess
import sys
import time
import traceback

ROOT = Path('/opt/anas-forgejo-account-native')
INPUTS = Path('/opt/anas-forgejo-account-inputs')
DATA = Path('/var/lib/gitea')
ACCOUNT = 'anas_actions_controller'
EMAIL = ACCOUNT + '@localhost.invalid'
FORGEJO_SHA256 = 'cb75c2780d13a8a8b91390e42615354219aac58fd86a9d20baa40f5281c8e9a3'
REQUIRED = (
    'isolated_vm_and_inputs', 'fixed_real_forgejo', 'separate_recovery_owner',
    'fresh_disabled_creates_no_account', 'unknown_same_name_not_adopted',
    'enabled_account_and_receipt', 'repeated_enable_preserves_password',
    'disabled_password_rejected_owner_preserved', 'repeated_disable_preserves_password',
    'reenabled_account_keeps_identity', 'restart_preserves_receipt_and_disabled_state',
    'replacement_id_not_adopted', 'disabled_controller_empty_state',
    'disabled_controller_retains_unfinished_work', 'disabled_controller_rejects_linked_state',
    'public_reports_exclude_credentials', 'real_server_exited',
)


class GateFailure(RuntimeError):
    def __init__(self, code):
        self.code = code if re.fullmatch(r'[a-z_]{1,64}', code) else 'unexpected_failure'
        super().__init__(self.code)


def require(ok, code):
    if not ok:
        raise GateFailure(code)


def validate_vm(identity, facts):
    require(re.fullmatch(r'anas-incus-host-[a-f0-9]{6}', identity) is not None and
            facts.get('uid') == 0 and facts.get('vendor') == 'QEMU' and
            facts.get('identity') == identity and facts.get('docker_absent') is True,
            'isolated_vm_required')


def complete(events):
    return (len(events) == len(REQUIRED) and {e.get('stage') for e in events} == set(REQUIRED)
            and all(e.get('status') == 'passed' for e in events))


def request_headers(username, password):
    headers = {'Accept': 'application/json'}
    if username:
        headers['Authorization'] = 'Basic '+base64.b64encode((username+':'+password).encode()).decode()
    return headers


def digest_input(path):
    require(path.is_absolute() and path.resolve() == path, 'input_path')
    for parent in path.parents:
        info = parent.lstat()
        require(stat.S_ISDIR(info.st_mode) and info.st_uid == 0 and not info.st_mode & 0o022, 'input_parent')
    info = path.lstat()
    require(stat.S_ISREG(info.st_mode) and info.st_uid == 0 and info.st_nlink == 1 and
            not info.st_mode & 0o022 and 0 < info.st_size < 256 << 20, 'input_file')
    with path.open('rb') as source:
        result = hashlib.file_digest(source, 'sha256').hexdigest()
        after = os.fstat(source.fileno())
    require((info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns) ==
            (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns), 'input_changed')
    return result


def write_new(path, body, mode=0o600):
    if isinstance(body, str):
        body = body.encode()
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_WRONLY, mode)
    with os.fdopen(fd, 'wb') as output:
        # The fixture's private umask must not remove execute permission from
        # the explicitly public product binaries run by UID 1000. Set only
        # the new descriptor's declared mode; secrets keep the 0600 default.
        os.fchmod(output.fileno(), mode)
        output.write(body)
        output.flush()
        os.fsync(output.fileno())


def run(identity):
    validate_vm(identity, {'uid': os.geteuid(), 'vendor': Path('/sys/class/dmi/id/sys_vendor').read_text().strip(),
                'identity': Path('/var/lib/cloud/data/instance-id').read_text().strip(),
                'docker_absent': not any(Path(p).exists() for p in ('/run/docker.sock', '/var/lib/docker'))})
    require(not ROOT.exists() and not DATA.exists(), 'fresh_data_required')
    digest_input(INPUTS/'source-manifest.json')
    manifest = json.loads((INPUTS/'source-manifest.json').read_bytes())
    require(manifest.get('schema') == 'anas.forgejo-account-native-inputs/v1' and
            set(manifest.get('files', {})) == {'forgejo', 'anas-forgejo-entrypoint',
                'anas-forgejo-actions-controller', Path(__file__).name}, 'input_manifest')
    for name, digest in manifest['files'].items():
        require(digest_input(INPUTS/name) == digest, 'input_digest')
    require(manifest['files']['forgejo'] == FORGEJO_SHA256 and
            digest_input(Path(__file__).resolve()) == manifest['files'][Path(__file__).name], 'source_identity')
    account = pwd.getpwnam('anas-test')
    require(account.pw_uid == account.pw_gid == 1000, 'unprivileged_service_account')
    with socket.socket() as check:
        check.bind(('127.0.0.1', 3000))
    ROOT.mkdir(mode=0o700)
    (ROOT/'private').mkdir(mode=0o700)
    (ROOT/'reports').mkdir(mode=0o700)
    for name in ('forgejo', 'anas-forgejo-entrypoint', 'anas-forgejo-actions-controller'):
        destination = Path('/usr/local/bin')/name
        require(not destination.exists() and not destination.is_symlink(), 'installed_binary_preexists')
        write_new(destination, (INPUTS/name).read_bytes(), 0o755)
    DATA.mkdir(mode=0o700)
    os.chown(DATA, 1000, 1000)
    for part in ('custom', 'custom/conf', 'home'):
        (DATA/part).mkdir(mode=0o700)
        os.chown(DATA/part, 1000, 1000)
    config = DATA/'custom/conf/app.ini'
    write_new(config, f'''APP_NAME = ANAS isolated account lifecycle
RUN_USER = anas-test
RUN_MODE = prod
[database]
DB_TYPE = sqlite3
PATH = {DATA}/forgejo.db
[repository]
ROOT = {DATA}/repositories
[server]
PROTOCOL = http
HTTP_ADDR = 127.0.0.1
HTTP_PORT = 3000
ROOT_URL = http://127.0.0.1:3000/
DISABLE_SSH = true
OFFLINE_MODE = true
APP_DATA_PATH = {DATA}/data
[security]
INSTALL_LOCK = true
PASSWORD_HASH_ALGO = pbkdf2
SECRET_KEY = {secrets.token_hex(32)}
INTERNAL_TOKEN = {secrets.token_hex(64)}
[service]
DISABLE_REGISTRATION = true
ENABLE_BASIC_AUTHENTICATION = true
[mailer]
ENABLED = false
[actions]
ENABLED = true
[cron]
ENABLED = false
[log]
MODE = console
LEVEL = Error
''')
    os.chown(config, 1000, 1000)
    env = {'PATH': '/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': str(DATA/'home'), 'USER': 'anas-test',
           'LC_ALL': 'C', 'GOMAXPROCS': '1', 'FORGEJO_WORK_DIR': str(DATA), 'FORGEJO_CUSTOM': str(DATA/'custom'),
           'GITEA_WORK_DIR': str(DATA), 'GITEA_CUSTOM': str(DATA/'custom')}
    events, private_values = [], []
    server, server_log = None, None
    stage = 'isolated_vm_and_inputs'

    def passed(name, **facts):
        event = {'stage': name, 'status': 'passed', **facts}
        events.append(event)
        print(json.dumps(event, sort_keys=True), flush=True)

    def command(label, argv, payload=None, expected=0, environment=None, failure_text=None):
        require(all(not value or all(value not in arg for arg in argv) for value in private_values), 'credential_in_argv')
        try:
            result = subprocess.run(argv, input=payload, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    cwd=DATA, env=env if environment is None else environment,
                                    user=1000, group=1000, extra_groups=[], timeout=45)
        except subprocess.TimeoutExpired as error:
            write_new(ROOT/'private'/(label+'.log'), (error.stdout or b'') + b'\n' + (error.stderr or b''))
            raise GateFailure('command_timeout') from None
        require(len(result.stdout) < 2 << 20 and len(result.stderr) < 2 << 20, 'command_output_bound')
        if (expected == 0 and result.returncode != 0) or (expected != 0 and result.returncode == 0):
            write_new(ROOT/'private'/(label+'.log'), result.stdout + b'\n' + result.stderr)
            raise GateFailure('command_failed')
        if failure_text is not None:
            require(failure_text.encode() in result.stderr, 'unexpected_failure_reason')
        return result.stdout

    def forgejo(label, *argv):
        return command(label, ['/usr/local/bin/forgejo', *argv, '--config', str(config), '--work-path', str(DATA)])

    def api(method, path, username, password, value=None):
        require(path.startswith('/api/v1/') and not any(c in path for c in ('\r', '\n', '#')), 'fixed_api_path')
        connection = http.client.HTTPConnection('127.0.0.1', 3000, timeout=10)
        try:
            headers = request_headers(username, password)
            body = None if value is None else json.dumps(value).encode()
            if body is not None:
                headers['Content-Type'] = 'application/json'
            connection.request(method, path, body=body, headers=headers)
            response = connection.getresponse()
            raw = response.read((1 << 20)+1)
            require(len(raw) <= 1 << 20, 'api_response_bound')
            return response.status, json.loads(raw) if raw else None
        finally:
            connection.close()

    def start_server():
        nonlocal server, server_log
        server_log = (ROOT/'private'/('server-'+secrets.token_hex(4)+'.log')).open('xb')
        server = subprocess.Popen(['/usr/local/bin/forgejo', 'web', '--config', str(config), '--work-path', str(DATA)],
                     stdin=subprocess.DEVNULL, stdout=server_log, stderr=server_log,
                     user=1000, group=1000, extra_groups=[], cwd=DATA, env=env)
        deadline = time.monotonic()+45
        while time.monotonic() < deadline:
            require(server.poll() is None, 'server_exited_early')
            try:
                code, body = api('GET', '/api/v1/version', '', '')
                if code == 200 and body['version'].split('+')[0] == '15.0.7':
                    return
            except (OSError, http.client.HTTPException):
                pass
            time.sleep(.2)
        raise GateFailure('server_readiness_timeout')

    def stop_server():
        nonlocal server, server_log
        if server is not None:
            if server.poll() is None:
                server.terminate()
            try:
                server.wait(timeout=20)
            except subprocess.TimeoutExpired:
                server.kill(); server.wait(timeout=5)
                raise GateFailure('forced_server_exit') from None
            require(server.returncode == 0, 'server_exit_failed')
            server = None
        if server_log is not None:
            server_log.close(); server_log = None

    try:
        passed(stage)
        stage = 'fixed_real_forgejo'
        version = command('version', ['/usr/local/bin/forgejo', '--version'])
        require(b'15.0.7' in version, 'forgejo_version')
        forgejo('migrate', 'migrate')
        start_server()
        passed(stage, version='15.0.7', service_uid=1000, database='isolated-sqlite')
        stage = 'separate_recovery_owner'
        owner, owner_password, password = 'admin_forgejo', secrets.token_hex(32), secrets.token_hex(32)
        private_values.extend((owner_password, password))
        owner_input = {'username': owner, 'email': owner+'@localhost.invalid', 'password': owner_password}
        command('owner', ['/usr/local/bin/anas-forgejo-entrypoint', 'local-admin'], json.dumps(owner_input).encode())
        status, owner_record = api('GET', '/api/v1/user', owner, owner_password)
        require(status == 200 and owner_record['login'] == owner and owner_record['is_admin'] is True, 'owner_unverified')
        owner_id = owner_record['id']
        passed(stage)

        def helper(enabled, label, expect_rejection=False):
            body = {'schema': 'anas.actions-account/v1', 'enabled': enabled, 'controller_password': password,
                    'manager_username': owner, 'manager_password': owner_password}
            return command(label, ['/usr/local/bin/anas-forgejo-entrypoint', 'actions-account'], json.dumps(body).encode(),
                           expected=1 if expect_rejection else 0)

        def user():
            return api('GET', '/api/v1/users/'+ACCOUNT, owner, owner_password)

        def check_account(expected_id, enabled):
            code, record = user()
            require(code == 200 and record['id'] == expected_id and record['login'] == ACCOUNT and record['email'] == EMAIL and
                    record['is_admin'] is True and expected_id != owner_id, 'account_identity')
            code, record = api('GET', '/api/v1/user', ACCOUNT, password)
            require((enabled and code == 200 and record['id'] == expected_id) or
                    (not enabled and code in (401, 403)), 'controller_password_state')
            code, record = api('GET', '/api/v1/user', owner, owner_password)
            require(code == 200 and record['id'] == owner_id and record['is_admin'] is True, 'owner_changed')

        receipt_path = DATA/'anas-actions-account/account.json'

        def receipt(expected_id, enabled):
            info = receipt_path.lstat()
            require(stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode) == 0o600 and info.st_uid == 1000 and info.st_nlink == 1, 'receipt_permissions')
            raw = receipt_path.read_bytes()
            require(len(raw) < 4096 and not any(secret.encode() in raw for secret in private_values), 'receipt_secret_leak')
            value = json.loads(raw)
            require(value['user_id'] == expected_id and value['state'] == ('enabled' if enabled else 'disabled'), 'receipt_identity')
            return raw

        def password_digest():
            # Read-only equality evidence, never export the salted hash/salt.
            with sqlite3.connect('file:'+str(DATA/'forgejo.db')+'?mode=ro', uri=True) as db:
                rows = db.execute('SELECT id, passwd, salt FROM user WHERE lower_name = ?', (ACCOUNT,)).fetchall()
            require(len(rows) == 1, 'db_identity')
            return hashlib.sha256(json.dumps(rows).encode()).hexdigest()

        def create_foreign():
            secret = secrets.token_hex(32); private_values.append(secret)
            code, value = api('POST', '/api/v1/admin/users', owner, owner_password,
                         {'username': ACCOUNT, 'email': EMAIL, 'password': secret, 'must_change_password': False})
            require(code == 201 and value['login'] == ACCOUNT and value['id'] != owner_id, 'foreign_fixture_creation')
            # Match the historic role as well: refusal must be based on the
            # missing receipt/password or changed ID, not merely a role check.
            code, value = api('PATCH', '/api/v1/admin/users/'+ACCOUNT, owner, owner_password, {'admin': True})
            require(code == 200 and value['is_admin'] is True, 'foreign_fixture_role')
            return value['id'], secret

        stage = 'fresh_disabled_creates_no_account'
        helper(False, 'fresh-disabled')
        require(user()[0] == 404 and not receipt_path.exists(), 'disabled_created_account')
        passed(stage)
        stage = 'unknown_same_name_not_adopted'
        foreign_id, foreign_password = create_foreign()
        before = password_digest()
        helper(True, 'foreign-enable', True); helper(False, 'foreign-disable', True)
        require(password_digest() == before and not receipt_path.exists() and
                api('GET', '/api/v1/user', ACCOUNT, foreign_password)[0] == 200, 'foreign_account_changed')
        code, row = user(); require(code == 200 and row['id'] == foreign_id, 'foreign_identity_changed')
        require(api('DELETE', '/api/v1/admin/users/'+ACCOUNT, owner, owner_password)[0] == 204, 'foreign_fixture_cleanup')
        passed(stage)
        stage = 'enabled_account_and_receipt'
        helper(True, 'enabled'); code, row = user(); require(code == 200, 'created_account_missing')
        ident = row['id']; check_account(ident, True); receipt(ident, True)
        passed(stage, account_id=ident)
        stage = 'repeated_enable_preserves_password'
        before = password_digest(); helper(True, 'enabled-again')
        require(password_digest() == before, 'enabled_password_rotated')
        check_account(ident, True); passed(stage)
        stage = 'disabled_password_rejected_owner_preserved'
        helper(False, 'disabled'); check_account(ident, False); receipt(ident, False)
        passed(stage)
        stage = 'repeated_disable_preserves_password'
        before = password_digest(); helper(False, 'disabled-again')
        require(password_digest() == before, 'disabled_password_rotated')
        check_account(ident, False); passed(stage)
        stage = 'reenabled_account_keeps_identity'
        helper(True, 'reenabled'); check_account(ident, True); receipt(ident, True)
        passed(stage, account_id=ident)
        stage = 'restart_preserves_receipt_and_disabled_state'
        helper(False, 'disabled-before-restart'); before = password_digest(); saved = receipt(ident, False)
        stop_server(); start_server()
        require(receipt(ident, False) == saved, 'restart_changed_receipt')
        helper(False, 'disabled-after-restart'); check_account(ident, False)
        require(password_digest() == before, 'restart_rotated_password')
        helper(True, 'reenabled-after-restart'); check_account(ident, True)
        passed(stage)
        stage = 'replacement_id_not_adopted'
        saved = receipt(ident, True)
        code, row = user(); require(code == 200 and row['id'] == ident, 'owned_identity_changed')
        require(api('DELETE', '/api/v1/admin/users/'+ACCOUNT, owner, owner_password)[0] == 204, 'owned_fixture_cleanup')
        replacement, replacement_password = create_foreign()
        require(replacement != ident, 'replacement_reused_id')
        before = password_digest()
        helper(False, 'replacement-disable', True); helper(True, 'replacement-enable', True)
        require(password_digest() == before and receipt_path.read_bytes() == saved and
                api('GET', '/api/v1/user', ACCOUNT, replacement_password)[0] == 200, 'replacement_adopted')
        passed(stage)
        stage = 'disabled_controller_empty_state'
        controller_dir = DATA/'controller-state'
        controller_dir.mkdir(mode=0o700); os.chown(controller_dir, 1000, 1000)
        state = controller_dir/'state.json'
        controller_env = {**env, 'FORGEJO_ACTIONS_ENABLED': 'false', 'FORGEJO_ACTIONS_STATE_PATH': str(state)}
        binary = ['/usr/local/bin/anas-forgejo-actions-controller']
        command('disabled-no-state', binary, environment=controller_env)
        require(not state.exists(), 'disabled_controller_wrote_state')
        write_new(state, '{"version":1,"workloads":{}}'); os.chown(state,1000,1000)
        before_state = state.read_bytes()
        command('disabled-empty-state', binary, environment=controller_env)
        require(state.read_bytes() == before_state, 'disabled_controller_changed_state')
        passed(stage)
        stage = 'disabled_controller_retains_unfinished_work'
        # Deliberate state fixture, not a claim about reclaiming a real guest.
        # Without a lease this process must exit unsuccessfully and keep it.
        state.write_text('{"version":1,"workloads":{"pending":{"handle":"pending"}}}')
        before_state = state.read_bytes()
        command('disabled-pending-state', binary, expected=1, environment=controller_env,
                failure_text='Actions is disabled but the compute lease is unavailable for cleanup')
        require(state.read_bytes() == before_state, 'pending_state_discarded')
        passed(stage)
        stage = 'disabled_controller_rejects_linked_state'
        # Preserve the pending fixture, adding only a deliberately invalid path.
        linked = controller_dir/'linked-state.json'
        linked.symlink_to(controller_dir/'absent-state.json')
        command('disabled-linked-state', binary, expected=1,
                environment={**controller_env, 'FORGEJO_ACTIONS_STATE_PATH':str(linked)},
                failure_text='controller state could not be safely read')
        require(linked.is_symlink() and state.read_bytes() == before_state, 'unsafe_state_overwritten')
        passed(stage)
        stage = 'public_reports_exclude_credentials'
        raw = json.dumps(events).encode()
        require(not any(secret.encode() in raw for secret in private_values), 'public_secret_leak')
        passed(stage)
        stage = 'real_server_exited'; stop_server(); passed(stage)
    except Exception as error:
        # Private diagnostic only; the public report retains fixed gate codes.
        write_new(ROOT/'private/failure.txt', traceback.format_exc())
        events.append({'stage': stage, 'status': 'failed', 'code': error.code if isinstance(error, GateFailure) else 'unexpected_failure'})
    finally:
        try:
            stop_server()
        except Exception as error:
            events.append({'stage': 'server_cleanup', 'status': 'failed', 'code': error.code if isinstance(error, GateFailure) else 'unexpected_failure'})
    result = {'schema': 'anas.forgejo-account-native/v1', 'vm_id': identity, 'events': events, 'passed': complete(events),
              'scope': 'real product helper + fixed Forgejo API + independent password/ID readback; not full Compose or guest-cleanup acceptance'}
    write_new(ROOT/'reports/summary.json', json.dumps(result, sort_keys=True, indent=2)+'\n')
    print(json.dumps(result, sort_keys=True), flush=True)
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--vm-id', required=True)
    args = parser.parse_args()
    os.umask(0o077)
    try:
        sys.exit(run(args.vm_id))
    except Exception:
        print(json.dumps({'passed': False, 'stage': 'fixture_preparation', 'code': 'fixture_rejected'}), flush=True)
        sys.exit(1)
