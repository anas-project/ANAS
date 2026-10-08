#!/usr/bin/env python3
"""Exercise the Module's actual Valkey health command on a disposable 4 MiB tmpfs."""
import json
import pathlib
import shlex
import subprocess
import tempfile
import time
import uuid

repo = pathlib.Path(__file__).resolve().parents[3]
prefix = 'anas-immich-valkey-it-' + uuid.uuid4().hex[:10]
root = pathlib.Path(tempfile.mkdtemp(prefix=prefix + '-'))
report = {'environment': 'isolated local Docker; bounded container tmpfs, not host disk exhaustion'}
container_created = False


def docker(*args, body=None, check=True):
    result = subprocess.run(['docker', *args], input=body, text=True, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, timeout=30)
    if check and result.returncode:
        raise RuntimeError('docker operation failed: ' + result.stderr[-2000:])
    return result


def eventually(condition, message, seconds=20):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if condition():
            return
        time.sleep(.25)
    raise AssertionError(message)


try:
    compose = json.loads(docker('compose', '-f', str(repo / 'modules/immich/docker-compose.yml'),
                                'config', '--no-interpolate', '--no-env-resolution', '--format', 'json').stdout)
    health = compose['services']['anas_immich_valkey']['healthcheck']['test']
    assert health[0] == 'CMD', 'fixture requires the Module executable health command'
    probe = health[1:]
    mirror = next(item for item in json.loads((repo / '.github/mirrors.json').read_text())
                  if item['image'] == 'anas-mirror-immich-valkey')
    image = mirror['source'] + '@' + mirror['digest']
    report['image_id'] = docker('image', 'inspect', '--format', '{{.Id}}', image).stdout.strip()
    report['health_command'] = probe
    docker('run', '-d', '--name', prefix, '--label', 'anas.test.immich=' + prefix,
           '--tmpfs', '/data:rw,size=4m', '--health-cmd', shlex.join(probe),
           '--health-interval', '1s', '--health-timeout', '2s', '--health-retries', '2',
           image, 'valkey-server', '--appendonly', 'yes', '--appendfsync', 'everysec')
    container_created = True
    eventually(lambda: docker('inspect', '--format', '{{.State.Health.Status}}', prefix).stdout.strip() == 'healthy',
               'writable queue did not become healthy')
    assert docker('exec', prefix, *probe).returncode == 0
    report['writable_queue_healthy'] = True
    # Only this fresh container's tmpfs can fill. No host filesystem or shared volume is targeted.
    for index in range(12):
        reply = docker('exec', '-i', prefix, 'valkey-cli', '--raw', '-x', 'SET',
                       'anas:test:filler:' + str(index), body='x' * (1024 * 1024)).stdout.strip()
        time.sleep(.25)
        if reply != 'OK':
            break
    eventually(lambda: 'aof_last_write_status:err' in docker('exec', prefix, 'valkey-cli',
                                                            '--raw', 'INFO', 'persistence').stdout,
               'bounded queue did not produce a real AOF ENOSPC error')
    old_probe = docker('exec', prefix, 'valkey-cli', 'ping')
    assert old_probe.returncode == 0 and 'MISCONF' in old_probe.stdout, 'fixed CLI false-success was not reproduced'
    current_probe = docker('exec', prefix, *probe, check=False)
    assert current_probe.returncode != 0, 'Module reports a failed queue write as healthy'
    eventually(lambda: docker('inspect', '--format', '{{.State.Health.Status}}', prefix).stdout.strip() == 'unhealthy',
               'Docker health state did not reject the full queue')
    report.update({'aof_enospc_confirmed': True, 'legacy_ping_exit_code': old_probe.returncode,
                   'module_probe_exit_code': current_probe.returncode, 'full_queue_unhealthy': True})
    print('PASS: actual Module Valkey write probe rejects real AOF ENOSPC and Docker marks queue unhealthy', flush=True)
except BaseException as error:
    report['failure'] = str(error)
    raise
finally:
    report_path = root / 'report.json'
    report_path.write_text(json.dumps(report, indent=2))
    report_path.chmod(0o600)
    print('REPORT: ' + str(report_path), flush=True)
    if container_created:
        docker('rm', '-f', prefix, check=False)
