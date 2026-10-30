# TEST_CASES: LLNG-READY-T-007
"""Run both providers on the explicitly isolated finance fixture and restore its services."""
import json, pathlib, subprocess, time, os
P = pathlib.Path('/home/whl/anas-llng-ready-test-20261023')
D = ['docker', '-H', 'unix:///run/anas-llng-test-1023.sock']
prefix = 'anas_llng_test_1023_'
os.umask(0o077)
names = ['llng', 'llng_dirwatch', 'casdoor', 'dirwatch', 'portal', 'nextcloud', 'vikunja']

def inspect(n):
    return json.loads(subprocess.check_output(D + ['inspect', prefix + n]))[0]

def ctl(verb, n):
    subprocess.run(D + [verb, prefix + n], check=True, stdout=subprocess.DEVNULL)

def ready(n, args, timeout=360):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if subprocess.run(D + ['exec', prefix + n] + args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            return
        time.sleep(2)
    raise RuntimeError(n + ' not ready')
original = {n: inspect(n)['State']['Running'] for n in names}
assert not original['casdoor'] and not original['dirwatch'], 'LLNG acceptance stack must be the starting state'
for n in names:
    assert inspect(n)['Name'] == '/' + prefix + n
a = json.loads((P / 'start-dirwatch.json').read_text())
old = str(P / 'events') + ':/var/lib/anas-directory-events:ro'
new = str(P / 'data/directory-events') + ':/var/lib/anas-directory-events:ro'
assert a.count(old) == 1
assert prefix + 'dirwatch' in a
if inspect('dirwatch')['State']['Running']:
    ctl('stop', 'dirwatch')
ctl('rm', 'dirwatch')
a[a.index(old)] = new
if subprocess.run(a, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
    raise RuntimeError('isolated Casdoor consumer creation failed')
ctl('stop', 'dirwatch')
try:
    for n in names:
        if inspect(n)['State']['Running']:
            ctl('stop', n)
    for provider, helper in [('casdoor', 'dirwatch'), ('llng', 'llng_dirwatch')]:
        ctl('restart', 'postgres')
        ready('postgres', ['pg_isready', '-U', 'postgres'])
        ctl('restart', 'samba')
        ready('samba', ['samba-tool', 'domain', 'info', '127.0.0.1'])
        ctl('restart', 'anchor')
        ctl('start', provider)
        ready(provider, ['test', '-f', '/run/llng-configured'] if provider == 'llng' else ['/opt/anas/bin/casdoor-helper', 'healthcheck'])
        ctl('start', helper)
        if provider == 'llng':
            ready(helper, ['/opt/anas/directory-watch.pl', '--healthcheck'])
        else:
            ready(helper, ['/opt/anas/bin/casdoor-helper', 'directory-watch', '--healthcheck'])
        time.sleep(10)
        with (P / 'evidence' / ('resources-' + provider + '-final.log')).open('w') as out:
            subprocess.run(['python3', str(P / 'llng-resource-benchmark.py'), provider], check=True, stdout=out, stderr=subprocess.STDOUT)
        print(provider + ' benchmark finished', flush=True)
        if provider != 'llng':
            ctl('stop', helper)
            ctl('stop', provider)
finally:
    for n in ['casdoor', 'dirwatch']:
        if inspect(n)['State']['Running'] and (not original[n]):
            ctl('stop', n)
    for n in ['llng', 'llng_dirwatch', 'portal', 'nextcloud', 'vikunja']:
        if original[n]:
            ctl('start', n)
            if n == 'llng':
                ready(n, ['test', '-f', '/run/llng-configured'])
    print('original application services restored', flush=True)
