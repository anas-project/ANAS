# TEST_CASES: LLNG-READY-T-005
"""Backup/empty-store recovery restricted to the disposable finance LLNG fixture.

Passwords, keys, database dumps and persistent sessions stay in a private directory.
Only counts, artifact hashes and non-secret database names are printed.
"""
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import sys
import time

root = pathlib.Path('/home/whl/anas-llng-ready-test-20261023')
docker = ['docker', '-H', 'unix:///run/anas-llng-test-1023.sock']
main, consumer, postgres = ['anas_llng_test_1023_' + name for name in ['llng', 'llng_dirwatch', 'postgres']]
backup = root / 'recovery-backup'
conf = root / 'data/llng/conf'
state = root / 'data/dirwatch'
restored_db = 'llng_restore_20261023'
os.umask(0o077)
action = sys.argv[1]
assert action in {'backup', 'restore', 'rollback', 'inventory', 'failed-restore'}
for name in [main, consumer]:
    info = json.loads(subprocess.check_output(docker + ['inspect', name]))[0]
    assert info['Config']['Labels']['anas.test.run'] == 'llng-ready-20261023'
    assert any(m['Source'] == str(conf) for m in info['Mounts'])

def run(args, **kw):
    return subprocess.run(args, check=True, **kw)

def control(verb, name):
    run(docker + [verb, name], stdout=subprocess.DEVNULL)

def inventory(database):
    result = {}
    for table in ['sessions', 'psessions', 'oidcsessions', 'samlsessions']:
        query = 'SELECT count(*), coalesce(md5(string_agg(id || a_session::text, \'\' ORDER BY id)), \'\') FROM ' + table
        text = subprocess.check_output(docker + ['exec', postgres, 'psql', '-U', 'postgres', '-d', database, '-Atc', query], text=True).strip()
        count, digest = text.split('|')
        result[table] = {'count': int(count), 'content_digest': digest}
    return result

def checked_dump(path, expected):
    data = path.read_bytes()
    if hashlib.sha256(data).hexdigest() != expected:
        raise ValueError('backup checksum mismatch; original store retained')
    return data

def require_empty_target(database):
    assert database in {'llng_restore_20261023', 'llng_failed_restore_20261023'}
    count = subprocess.check_output(docker + ['exec', postgres, 'psql', '-U', 'postgres', '-Atc',
        "SELECT count(*) FROM pg_database WHERE datname='" + database + "'"], text=True).strip()
    if count != '0':
        raise ValueError('recovery target already exists; refusing to overwrite it')

def await_ready():
    deadline = time.monotonic() + 360
    while time.monotonic() < deadline:
        if subprocess.run(docker + ['exec', main, 'test', '-f', '/run/llng-configured'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            return
        time.sleep(2)
    raise RuntimeError('restored LLNG configuration did not finish; private backup retained')

def start_from_config():
    run(['python3', str(root / 'recreate-llng-passkey.py')], stdout=subprocess.DEVNULL)
    await_ready()
    control('rm', consumer)
    run(['python3', str(root / 'start-directory-consumer.py')], stdout=subprocess.DEVNULL)

env = json.loads((root / 'llng-env.json').read_text())
if action == 'failed-restore':
    work = root / 'recovery-failure'
    work.mkdir(mode=0o700)
    manifest = json.loads((backup / 'manifest.json').read_text())
    data = checked_dump(backup / 'database.dump', manifest['dump_sha256'])
    broken = work / 'truncated.dump'
    broken.write_bytes(data[:len(data) // 2])
    rejected = False
    try:
        checked_dump(broken, manifest['dump_sha256'])
    except ValueError:
        rejected = True
    assert rejected, 'the same restore checksum guard must reject a damaged archive'
    failed_db = 'llng_failed_restore_20261023'
    require_empty_target(failed_db)
    original_env = (root / 'llng-env.json').read_bytes()
    original_conf = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in conf.glob('lmConf-*.json')}
    control('stop', consumer)
    control('stop', main)
    try:
        before = inventory(env['LLNG_DB_NAME'])
        run(docker + ['exec', postgres, 'createdb', '-U', 'postgres', failed_db])
        existing_rejected = False
        try:
            require_empty_target(failed_db)
        except ValueError:
            existing_rejected = True
        assert existing_rejected
        # Deliberately bypass the checked importer to exercise native partial
        # import failure in an empty target as a second, independent boundary.
        with broken.open('rb') as source:
            result = subprocess.run(docker + ['exec', '-i', postgres, 'pg_restore', '-U', 'postgres',
                '--no-owner', '--no-privileges', '--exit-on-error', '-d', failed_db], stdin=source, capture_output=True)
        (work / 'private-import.log').write_bytes(result.stdout + result.stderr)
        assert result.returncode != 0, 'native importer must reject a truncated archive'
        assert inventory(env['LLNG_DB_NAME']) == before
        assert (root / 'llng-env.json').read_bytes() == original_env
        assert {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in conf.glob('lmConf-*.json')} == original_conf
        report = {'checksum_rejected': True, 'native_import_rejected': True, 'existing_target_rejected': True,
                  'original_tables_preserved': True, 'original_configuration_preserved': True,
                  'failed_database_retained': failed_db}
        (work / 'manifest.json').write_text(json.dumps(report))
    finally:
        control('start', main)
        await_ready()
        control('start', consumer)
    print(json.dumps(report))
elif action == 'inventory':
    print(json.dumps({'database': env['LLNG_DB_NAME'], 'tables': inventory(env['LLNG_DB_NAME'])}))
elif action == 'backup':
    assert not backup.exists(), 'retain the previous backup rather than overwrite it'
    backup.mkdir(mode=0o700)
    control('stop', consumer)
    control('stop', main)
    try:
        shutil.copytree(conf, backup / 'conf')
        shutil.copytree(state, backup / 'dirwatch')
        for name in ['llng-env.json', 'oidc.key', 'oidc.pub']:
            shutil.copy2(root / name, backup / name)
        with (backup / 'database.dump').open('wb') as output:
            run(docker + ['exec', postgres, 'pg_dump', '-U', 'postgres', '--no-owner', '--no-privileges', '-Fc', env['LLNG_DB_NAME']], stdout=output)
        report = {'database': env['LLNG_DB_NAME'], 'tables': inventory(env['LLNG_DB_NAME']),
                  'dump_sha256': hashlib.sha256((backup / 'database.dump').read_bytes()).hexdigest(),
                  'public_key_sha256': hashlib.sha256((backup / 'oidc.pub').read_bytes()).hexdigest(),
                  'pending_sha256': hashlib.sha256((backup / 'dirwatch/pending.json').read_bytes()).hexdigest(),
                  'pending_count': len(json.loads((backup / 'dirwatch/pending.json').read_text())['pending'])}
        (backup / 'manifest.json').write_text(json.dumps(report))
        print(json.dumps(report))
    finally:
        control('start', main)
        await_ready()
        control('start', consumer)
elif action == 'restore':
    manifest = json.loads((backup / 'manifest.json').read_text())
    assert env['LLNG_DB_NAME'] == manifest['database']
    checked_dump(backup / 'database.dump', manifest['dump_sha256'])
    assert not (root / 'data/llng/conf-before-recovery').exists()
    require_empty_target(restored_db)
    control('stop', consumer)
    control('stop', main)
    run(docker + ['exec', postgres, 'createdb', '-U', 'postgres', restored_db])
    with (backup / 'database.dump').open('rb') as source:
        run(docker + ['exec', '-i', postgres, 'pg_restore', '-U', 'postgres', '--no-owner', '--no-privileges', '--exit-on-error', '-d', restored_db], stdin=source)
    actual = inventory(restored_db)
    assert actual == manifest['tables'], 'restore must preserve every session and persistent credential row'
    conf.rename(root / 'data/llng/conf-before-recovery')
    shutil.copytree(backup / 'conf', conf)
    for path in conf.glob('lmConf-*.json'):
        value = json.loads(path.read_text())
        for section in ['globalStorageOptions', 'persistentStorageOptions', 'oidcStorageOptions', 'samlStorageOptions', 'casStorageOptions', 'notificationStorageOptions']:
            for key in ['DataSource', 'dbiChain']:
                if key in value.get(section, {}):
                    value[section][key] = value[section][key].replace('database=' + manifest['database'] + ';', 'database=' + restored_db + ';')
        path.write_text(json.dumps(value))
    state.rename(root / 'data/dirwatch-before-recovery')
    shutil.copytree(backup / 'dirwatch', state)
    assert hashlib.sha256((state / 'pending.json').read_bytes()).hexdigest() == manifest['pending_sha256']
    for path in [state, *state.rglob('*')]: os.chown(path, 33, 33)
    env['LLNG_DB_NAME'] = restored_db
    (root / 'llng-env.json').write_text(json.dumps(env))
    start_from_config()
    print(json.dumps({'restored_database': restored_db, 'empty_target': True, 'tables_match': True, 'original_retained': True, 'pending_preserved': manifest['pending_count']}))
else:
    assert (root / 'data/llng/conf-before-recovery').is_dir()
    control('stop', consumer)
    control('stop', main)
    # Preserve the failed recovery for diagnosis, including new credential counters.
    conf.rename(root / 'data/llng/conf-recovery-at-rollback')
    (root / 'data/llng/conf-before-recovery').rename(conf)
    state.rename(root / 'data/dirwatch-recovery-at-rollback')
    (root / 'data/dirwatch-before-recovery').rename(state)
    shutil.copy2(backup / 'llng-env.json', root / 'llng-env.json')
    start_from_config()
    print(json.dumps({'rolled_back': True, 'recovery_artifacts_retained': True}))
