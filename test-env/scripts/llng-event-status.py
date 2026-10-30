# TEST_CASES: LLNG-READY-T-006
# TEST_CASES: LLNG-READY-T-001
"""Read-only sanitized status for the explicitly isolated LLNG acceptance stack."""
import json
import pathlib
import hashlib
import re
import subprocess
import sys

root = pathlib.Path('/home/whl/anas-llng-ready-test-20261023')
docker = ['docker', '-H', 'unix:///run/anas-llng-test-1023.sock']
result = {}
if '--session-client' in sys.argv:
    request = json.load(sys.stdin)
    cookie = request['cookie']
    assert re.fullmatch(r'[0-9a-f]{32,128}', cookie)
    env = json.loads((root / 'llng-env.json').read_text())
    query = "SELECT json_build_object('id',id,'clients',a_session->>'_oidcConnectedRPIDs') FROM sessions WHERE a_session->>'_session_kind' = 'SSO'"
    rows = subprocess.check_output(docker + ['exec', 'anas_llng_test_1023_postgres', 'psql', '-U', 'postgres', '-d', env['LLNG_DB_NAME'], '-Atc', query], text=True)
    matches = [json.loads(row) for row in rows.splitlines() if json.loads(row)['id'] in {cookie, hashlib.sha256(cookie.encode()).hexdigest()}]
    print(json.dumps({'exists': bool(matches), 'clients': (matches[0]['clients'] or '').split(',') if matches else []}))
    sys.exit(0)
for key, path in [('producer', 'data/anchor-state/health.json'), ('consumer', 'data/dirwatch/health.json'), ('cursor', 'data/dirwatch/cursor.json')]:
    try:
        value = json.loads((root / path).read_text())
        result[key] = {k: value[k] for k in ['ready', 'last_published_seq', 'pending', 'cursor', 'reconciled', 'checked', 'limited_clients'] if k in value}
    except (FileNotFoundError, ValueError):
        result[key] = {'unavailable': True}
result['configured'] = (root / 'data/llng/conf/.anas-ready').exists()
r = subprocess.run(docker + ['inspect', '--format', '{{.State.Running}}', 'anas_llng_test_1023_llng_dirwatch'], capture_output=True, text=True)
result['consumer']['running'] = r.returncode == 0 and r.stdout.strip() == 'true'
if '--sessions' in sys.argv:
    identities = json.loads((root / 'data/samba/llng-portal-fixture.json').read_text())['users']
    names = {value['anchor']: name for name, value in identities.items()}
    pending = json.loads((root / 'data/dirwatch/pending.json').read_text())['pending']
    result['notifications'] = {}
    for action in pending.values():
        if action.get('type') != 'notify': continue
        rp = action['rp']; name = names.get(action['session']['anasIdentityAnchor'], 'other')
        counts = result['notifications'].setdefault(rp, {})
        counts[name] = counts.get(name, 0) + 1
    env = json.loads((root / 'llng-env.json').read_text())
    query = "SELECT a_session->>'anasIdentityAnchor', coalesce(a_session->>'_oidcConnectedRPIDs', ''), count(*) FROM sessions WHERE a_session->>'_session_kind' = 'SSO' GROUP BY 1, 2"
    rows = subprocess.check_output(docker + ['exec', 'anas_llng_test_1023_postgres', 'psql', '-U', 'postgres', '-d', env['LLNG_DB_NAME'], '-Atc', query], text=True)
    result['sso_counts'] = {name: 0 for name in identities}
    result['oidc_connections'] = {name: {} for name in identities}
    for line in rows.splitlines():
        anchor, clients, count = line.split('|')
        if anchor not in names: continue
        name = names[anchor]
        result['sso_counts'][name] += int(count)
        for client in filter(None, clients.split(',')):
            counts = result['oidc_connections'][name]
            counts[client] = counts.get(client, 0) + int(count)
print(json.dumps(result))
