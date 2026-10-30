# TEST_CASES: LLNG-READY-T-001
"""Fault controls restricted to the disposable finance LLNG test namespace."""
import json
import os
import pathlib
import shutil
import subprocess
import sys

root = pathlib.Path('/home/whl/anas-llng-ready-test-20261023')
docker = ['docker', '-H', 'unix:///run/anas-llng-test-1023.sock']
action = sys.argv[1]
assert action in {'block_callback', 'restore_callback', 'restart_consumer', 'stop_consumer',
                  'start_consumer', 'partial_journal', 'complete_journal', 'rotated_duplicate',
                  'retained_gap', 'corrupt_cursor', 'restore_journal'}
container = 'anas_llng_test_1023_llng_dirwatch'
info = json.loads(subprocess.check_output(docker + ['inspect', container]))[0]
assert info['Config']['Labels']['anas.test.run'] == 'llng-ready-20261023'
assert any(m['Source'] == str(root / 'data/dirwatch') for m in info['Mounts'])
def control(verb, name):
    subprocess.run(docker + [verb, name], check=True, stdout=subprocess.DEVNULL)

events = root / 'data/directory-events'
journal = events / 'events.jsonl'
backup = root / 'journal-fault-backup'
producer = 'anas_llng_test_1023_anchor'
if action in {'restart_consumer', 'stop_consumer', 'start_consumer'}:
    control(action.split('_')[0], container)
elif action in {'partial_journal', 'rotated_duplicate', 'retained_gap', 'corrupt_cursor'}:
    assert not info['State']['Running'], 'stop the disposable consumer before the journal fault'
    assert not backup.exists(), 'restore the previous fault before starting another'
    producer_info = json.loads(subprocess.check_output(docker + ['inspect', producer]))[0]
    assert producer_info['Config']['Labels']['anas.test.run'] == 'llng-ready-20261023'
    assert any(m['Source'] == str(events) for m in producer_info['Mounts'])
    control('stop', producer)
    backup.mkdir(mode=0o700)
    for name in ['events.jsonl', 'events.jsonl.1']:
        if (events / name).exists():
            shutil.copy2(events / name, backup / name)
    cursor_path = root / 'data/dirwatch/cursor.json'
    cursor = int(json.loads(cursor_path.read_text())['cursor'])
    rows = {}
    for path in backup.iterdir():
        for line in path.read_bytes().splitlines():
            row = json.loads(line)
            rows[int(row['seq'])] = line + b'\n'
    later = sorted(seq for seq in rows if seq > cursor)
    assert later, 'the fault must use a new real Samba audit event'
    rotated = events / 'events.jsonl.1'
    if rotated.exists():
        rotated.unlink()
    if action == 'partial_journal':
        assert later[0] == cursor + 1
        complete = b''.join(rows[seq] for seq in sorted(rows) if seq <= cursor)
        journal.write_bytes(complete + rows[later[0]][:len(rows[later[0]]) // 2])
    elif action == 'rotated_duplicate':
        rotated.write_bytes(b''.join(rows[seq] for seq in sorted(rows)))
        os.chown(rotated, 65534, 65534)
        rotated.chmod(0o644)
        journal.write_bytes(rows[max(rows)])
    elif action == 'retained_gap':
        assert later[-1] > cursor + 1, 'retention must skip at least one real event'
        journal.write_bytes(rows[later[-1]])
    else:
        cursor_path.write_text('{')
    control('start', container)
elif action in {'complete_journal', 'restore_journal'}:
    assert backup.is_dir(), 'no journal fault is active'
    for name in ['events.jsonl', 'events.jsonl.1']:
        path = events / name
        if (backup / name).exists():
            path.write_bytes((backup / name).read_bytes())
            os.chown(path, 65534, 65534)
            path.chmod(0o644)
        elif path.exists():
            path.unlink()
    if action == 'restore_journal':
        shutil.rmtree(backup)
        control('start', producer)
        control('start', container)
else:
    path = root / 'traefik.json'
    config = json.loads(path.read_text())
    assert config['http']['routers']['portal']['rule'] == 'Host(`portal.llngtest.invalid`)'
    if action == 'block_callback':
        config['http']['routers']['logout-fault'] = {
            'rule': 'Host(`portal.llngtest.invalid`) && Path(`/oidc/backchannel-logout`)',
            'priority': 10000, 'entryPoints': ['https'], 'tls': {}, 'service': 'logout-fault',
        }
        config['http']['services']['logout-fault'] = {'loadBalancer': {'servers': [{'url': 'http://127.0.0.1:18089'}]}}
    else:
        config['http']['routers'].pop('logout-fault', None)
        config['http']['services'].pop('logout-fault', None)
    # Keep the inode: this test proxy mounts a single dynamic config file.
    path.write_text(json.dumps(config))
    subprocess.run(docker + ['restart', 'anas_llng_test_1023_traefik'], check=True, stdout=subprocess.DEVNULL)
print(json.dumps({'fixture_action': action}))
