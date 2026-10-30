# TEST_CASES: MAILMOD-T-017 MAILMOD-T-081
"""Run the administrator/socket fixture on a leased isolated Docker daemon."""
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys

if len(sys.argv) != 4:
    raise SystemExit('private directory, host SHA-256 and service SHA-256 required')
work = Path(sys.argv[1])
if not work.is_absolute() or not work.name.startswith('anas-outbound-admin-') or work.is_symlink() or work.stat().st_uid != os.getuid() or work.stat().st_mode & 0o077:
    raise SystemExit('private fixture directory required')
host, service = work/'host.test', work/'service.test'
for path, digest in ((host, sys.argv[2]), (service, sys.argv[3])):
    if path.is_symlink() or not path.is_file() or path.stat().st_uid != os.getuid() or hashlib.sha256(path.read_bytes()).hexdigest() != digest:
        raise SystemExit('test binary checksum mismatch')
    path.chmod(0o700)
base = Path('/home/whl/anas-mail-go-test-20261024')
values = {}
for line in (base/'daemon.env').read_text().splitlines():
    if line.startswith('export '):
        key, value = line[7:].split('=', 1)
        values[key] = shlex.split(value)[0]
if values['ANAS_TEST_DOCKER_SOCKET'] != '/run/anas-mail-test-1024.sock' or values['ANAS_TEST_DOCKER_ROOT'] != str(base/'docker') or values['ANAS_TEST_NETNS'] != 'anas-mail-test-1024' or Path(values['ANAS_TEST_DOCKER_SOCKET']).exists():
    raise SystemExit('dedicated Docker daemon state not suitable')
daemon = ['sudo', '-n', 'env'] + [k+'='+v for k, v in values.items()] + ['sh', str(base/'server-isolated-docker.sh')]

def run(args, timeout=120):
    return subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)

def save(name, content):
    path = work/name
    path.write_bytes(content)
    path.chmod(0o600)

def business():
    p = run(['sudo', '-n', '/usr/bin/docker', '-H', 'unix:///var/run/docker.sock', 'ps', '-a', '--format', '{{.ID}} {{.Names}}'])
    if p.returncode:
        raise RuntimeError('business inventory unavailable')
    return sorted(p.stdout.decode().splitlines())

before = business()
failure, cleanup, started = None, [], False
try:
    p = run(daemon+['start'])
    started = True
    save('daemon-start.log', p.stdout)
    if p.returncode:
        raise RuntimeError('isolated daemon start failed')
    p = run(['sudo', '-n', 'env', 'DOCKER_HOST=unix:///run/anas-mail-test-1024.sock', 'MAIL_OUTBOUND_ADMIN_LIVE=1', 'MAIL_OUTBOUND_SERVICE_TEST='+str(service), str(host), '-test.v', '-test.run', '^TestMailOutboundAdminSocketLive$', '-test.timeout', '100s'])
    save('test.log', p.stdout)
    if p.returncode:
        raise RuntimeError('isolated administrator test failed')
except BaseException as exc:
    failure = type(exc).__name__ + ': ' + str(exc)
finally:
    if started:
        try:
            p = run(daemon+['stop'])
            save('daemon-stop.log', p.stdout)
            if p.returncode:
                cleanup.append('isolated daemon stop failed')
        except BaseException:
            cleanup.append('isolated daemon stop unknown')
    try:
        unchanged = business() == before
    except BaseException:
        unchanged = False
    if not unchanged:
        cleanup.append('business inventory changed or unknown')
    result = {'failure': failure, 'cleanup_errors': cleanup, 'business_unchanged': unchanged}
    save('final.json', json.dumps(result, indent=2).encode())
    print(json.dumps(result), flush=True)
if failure or cleanup:
    raise SystemExit(1)
