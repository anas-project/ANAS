#!/usr/bin/env python3
# TEST_CASES: PORTAL-T-002, PORTAL-T-003, PORTAL-T-004
"""Run the existing portal cases inside their owned Docker namespace."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys

root = Path(sys.argv[1])
mode = sys.argv[2]
assert re.fullmatch(r'/home/whl/anas-user-portal-e2e-test-[A-Za-z0-9-]+', str(root))
assert root.resolve() == root and mode in ['dependencies', 'browser', 'faults', 'boundary']
run = json.loads((root / 'run.json').read_text())
assert run['remote_root'] == str(root) and run['socket'] == '/run/' + root.name + '.sock'
assert re.fullmatch(r'anas_pu[0-9a-f]{8}_', run['prefix'])
assert type(run['port']) is int and 1 <= run['port'] <= 65535
socket = 'unix://' + run['socket']
image = 'mcr.microsoft.com/playwright@sha256:dcc5531e97840b9b5e794f2814476b21571c5124a3fca2267d73041f56e7580e'
def docker(*args):
    return subprocess.check_output(['docker', '-H', socket, *args])
facts = json.loads(docker('info', '--format', '{{json .}}'))
assert Path(facts['DockerRootDir']) == root / 'docker'
install = root / 'browser-install'
if mode == 'dependencies':
    assert (install / 'package-lock.json').is_file()
    subprocess.run(['docker', '-H', socket, 'run', '--rm', '--name', run['prefix'] + 'browser_dependencies',
                    '--network', 'host', '--memory', '768m', '--workdir', '/install',
                    '-v', str(install) + ':/install', image,
                    'npm', 'ci', '--ignore-scripts', '--no-audit', '--no-fund',
                    '--registry=https://registry.npmmirror.com'], check=True)
else:
    assert json.loads((install / 'node_modules/@playwright/test/package.json').read_text())['version'] == '1.62.1'
    assert (root / 'private').stat().st_mode & 0o077 == 0
    args = ['docker', '-H', socket, 'run', '--rm', '--init', '--name', run['prefix'] + 'browser',
            '--network', 'host', '--memory', '1536m', '--pids-limit', '512', '--shm-size', '256m',
            '--read-only', '--tmpfs', '/tmp:mode=1777,size=512m', '--cap-drop', 'ALL',
            '--cap-add', 'DAC_OVERRIDE', '--security-opt', 'no-new-privileges:true',
            '-v', str(root) + ':' + str(root) + ':ro',
            '-v', str(root / 'private') + ':' + str(root / 'private'),
            '-v', str(root / 'evidence') + ':' + str(root / 'evidence'),
            '-v', str(root / 'workspace/data/user_portal/audit') + ':' + str(root / 'workspace/data/user_portal/audit'),
            '-v', str(install / 'node_modules') + ':/node_modules:ro',
            '-v', '/usr/bin/docker:/usr/bin/docker:ro', '-v', run['socket'] + ':' + run['socket']]
    env = {'DOCKER_HOST': socket, 'PORTAL_PROBE_ROOT': str(root), 'PORTAL_TEST_REMOTE_ROOT': str(root),
           'PORTAL_TEST_LOCAL_FIXTURE': '1', 'PORTAL_TEST_RESOLVED_IP': run['ip'],
           'PORTAL_TEST_ORIGIN': 'https://portal.portal.test:' + str(run['port']),
           'PORTAL_TEST_RECENT_WINDOW_SECONDS': '60', 'PORTAL_TEST_MODE': mode}
    for key, value in env.items():
        args += ['-e', key + '=' + value]
    args += [image, 'node', str(root / 'source/test-env/playwright/user-portal-e2e.mjs')]
    subprocess.run(args, check=True)
    if mode == 'faults':
        subprocess.run([sys.executable, str(root / 'source/test-env/scripts/user-portal-runtime-fault.py'),
                        str(root), 'audit-contract'], check=True)
