#!/usr/bin/env python3
# TEST_CASES: MAILMOD-T-083
"""Run the mail backup stop barrier on the dedicated finance Docker daemon."""
import hashlib
import json
import os
import shlex
import subprocess
import sys
from pathlib import Path

if len(sys.argv) != 3:
    raise SystemExit('private fixture directory and binary SHA-256 required')
work = Path(sys.argv[1])
if (not work.is_absolute() or not work.name.startswith('anas-mail-backup-stop-') or work.is_symlink()
        or work.stat().st_uid != os.getuid() or work.stat().st_mode & 0o077):
    raise SystemExit('private fixture directory required')
binary = work/'runner.test'
if binary.is_symlink() or hashlib.sha256(binary.read_bytes()).hexdigest() != sys.argv[2]:
    raise SystemExit('fixture binary checksum mismatch')
binary.chmod(0o700)
base = Path('/home/whl/anas-mail-go-test-20261024')
settings = {}
for line in (base/'daemon.env').read_text().splitlines():
    if line.startswith('export '):
        key, value = line[7:].split('=', 1)
        settings[key] = shlex.split(value)[0]
if (settings['ANAS_TEST_DOCKER_SOCKET'] != '/run/anas-mail-test-1024.sock'
        or settings['ANAS_TEST_DOCKER_ROOT'] != str(base/'docker')
        or settings['ANAS_TEST_NETNS'] != 'anas-mail-test-1024'
        or Path(settings['ANAS_TEST_DOCKER_SOCKET']).exists()):
    raise SystemExit('dedicated daemon is not in the expected stopped state')
daemon = ['sudo', '-n', 'env'] + [k+'='+v for k, v in settings.items()] + ['sh', str(base/'server-isolated-docker.sh')]
def run(args, timeout=300):
    return subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
def save(name, data):
    path = work/name
    path.write_bytes(data)
    path.chmod(0o600)
def business():
    result = run(['sudo', '-n', 'docker', 'ps', '-a', '--format', '{{.ID}} {{.Names}}'])
    if result.returncode:
        raise RuntimeError('business inventory unavailable')
    return sorted(result.stdout.decode().splitlines())
def cleanup_test_containers(fixture_root):
    # The daemon is dedicated to this test. Select only Compose containers
    # whose frozen working directory is inside this exact fixture's tmp tree.
    docker = ['sudo', '-n', 'docker', '-H', 'unix:///run/anas-mail-test-1024.sock']
    listing = run(docker+['ps', '-a', '--format', '{{.ID}}|{{.Label "com.docker.compose.project"}}|{{.Label "com.docker.compose.project.working_dir"}}'], 15)
    if listing.returncode:
        raise RuntimeError('isolated container inventory unavailable')
    prefix = str(fixture_root/'tmp') + '/'
    for line in listing.stdout.decode().splitlines():
        fields = line.split('|')
        if len(fields) != 3:
            raise RuntimeError('invalid isolated container inventory')
        identity, project, directory = fields
        if not (project.startswith('anasbk') and project.endswith('_mail') and directory.startswith(prefix)
                and directory.endswith('/.anas/deployments/backup-test/modules/mail')):
            continue
        check = run(docker+['inspect', '--format', '{{.Config.Image}}|{{.HostConfig.NetworkMode}}|{{len .HostConfig.PortBindings}}|{{range .Mounts}}{{.Type}},{{end}}', identity], 15)
        if check.returncode:
            raise RuntimeError('isolated test container identity unavailable')
        image, network, ports, mounts = check.stdout.decode().strip().split('|')
        if (image != 'ghcr.io/anas-project/anas-postgres:18.4.0-r5' or network != 'none' or ports != '0'
                or any(kind and kind != 'volume' for kind in mounts.split(','))):
            raise RuntimeError('unexpected isolated test container configuration')
        if run(docker+['rm', '-f', '-v', identity], 15).returncode:
            raise RuntimeError('isolated test container removal failed')
before = business()
started = False
failure = None
cleanup = []
try:
    started = True
    result = run(daemon+['start'])
    save('daemon-start.log', result.stdout)
    if result.returncode:
        raise RuntimeError('isolated daemon start failed')
    temporary = work/'tmp'
    temporary.mkdir(mode=0o700)
    result = run(['sudo', '-n', 'env', 'DOCKER_HOST=unix:///run/anas-mail-test-1024.sock',
                  'ANAS_BACKUP_STOP_NATIVE=1', 'TMPDIR='+str(temporary), str(binary),
                  '-test.v', '-test.run', '^TestBackupStopConfirmationNative$', '-test.timeout', '8m'], 510)
    save('native.log', result.stdout)
    if result.returncode or b'--- PASS: TestBackupStopConfirmationNative ' not in result.stdout:
        raise RuntimeError('isolated native backup stop test failed or did not run')
except BaseException as exc:
    failure = type(exc).__name__+': '+str(exc)
finally:
    if started:
        try:
            cleanup_test_containers(work)
        except BaseException:
            cleanup.append('isolated test container cleanup failed or unknown')
        try:
            result = run(daemon+['stop'])
            save('daemon-stop.log', result.stdout)
            if result.returncode:
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
