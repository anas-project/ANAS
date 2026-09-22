"""Execute the unchanged guest starter with test-only commands on PATH.

The mock install refuses the first filesystem effect. No /run, user, systemd,
container, or network is changed. timeout remains the actual coreutils program.
These tests verify admission/order, not a real Podman or one-job execution.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

STARTER = Path(__file__).resolve().parents[2] / 'modules/forgejo/runner-image/anas-forgejo-runner-start'
TOKEN = 'b' * 40
DISPATCH = r'''
import json, os, pathlib, subprocess, sys, time
root = pathlib.Path(os.environ['FIXTURE_ROOT'])
name = pathlib.Path(sys.argv[0]).name
def record(**fields):
    with (root/'trace.jsonl').open('a') as out:
        out.write(json.dumps({'command': name, **fields})+'\n')
if name == 'systemctl':
    record(arguments=sys.argv[1:])
    sys.exit(0 if os.environ.get('ALREADY_ACTIVE') == '1' else 1)
if name == 'install':
    record(input_bytes=len(sys.stdin.buffer.read()))
    sys.exit(77) # Never perform any real guest filesystem operation.
if name == 'sleep':
    record(arguments=sys.argv[1:])
    sys.exit(0) # Count scheduled delays without slowing deterministic cases.
if name == 'timeout':
    record(arguments=sys.argv[1:])
    binary = os.environ['REAL_TIMEOUT']
    os.execv(binary, [binary, *sys.argv[1:]])
if name == 'runuser':
    attempt = int((root/'attempt').read_text()) if (root/'attempt').exists() else 0
    (root/'attempt').write_text(str(attempt+1))
    record(arguments=sys.argv[1:], input_bytes=len(sys.stdin.buffer.read()))
    cases = json.loads((root/'replies.json').read_text())
    case = cases[min(attempt, len(cases)-1)]
    print(case.get('output', ''), flush=True)
    print('private-engine-diagnostic-marker', file=sys.stderr, flush=True)
    time.sleep(case.get('sleep', 0))
    sys.exit(case['exit'])
raise SystemExit('unexpected test command')
'''


class RunnerAdmission(unittest.TestCase):
    def invoke(self, replies, *, active=False, arguments=None):
        real_timeout = shutil.which('timeout') or shutil.which('gtimeout')
        self.assertIsNotNone(real_timeout, 'coreutils timeout is required; do not silently skip admission tests')
        with tempfile.TemporaryDirectory(prefix='anas-runner-admission-') as directory:
            root = Path(directory)
            commands = root/'bin'
            commands.mkdir()
            dispatch = root/'dispatch'
            dispatch.write_text('#!'+sys.executable+'\n'+DISPATCH)
            dispatch.chmod(0o700)
            for name in ('timeout', 'runuser', 'install', 'systemctl', 'sleep'):
                (commands/name).symlink_to(dispatch)
            (root/'replies.json').write_text(json.dumps(replies))
            env = {'PATH': str(commands)+':/usr/bin:/bin', 'FIXTURE_ROOT': str(root),
                   'REAL_TIMEOUT': real_timeout, 'ALREADY_ACTIVE': '1' if active else '0'}
            args = arguments if arguments is not None else ['--url', 'https://forgejo.test', '--uuid', 'runner-id',
                                                          '--handle', 'job-id', '--label', 'docker:docker://node:24']
            result = subprocess.run(['/bin/sh', str(STARTER), *args], env=env, input=TOKEN,
                                    text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=12)
            trace = [json.loads(line) for line in (root/'trace.jsonl').read_text().splitlines()] if (root/'trace.jsonl').exists() else []
        self.assertNotIn(TOKEN, result.stdout+result.stderr)
        self.assertNotIn('private-engine-diagnostic-marker', result.stdout+result.stderr)
        return result, trace

    def test_unavailable_engine_never_reaches_token_filesystem(self):
        result, trace = self.invoke([{'exit': 125}])
        self.assertEqual(result.returncode, 69)
        self.assertFalse(any(e['command'] == 'install' for e in trace))
        self.assertEqual(sum(e['command'] == 'runuser' for e in trace), 8)
        self.assertEqual(sum(e['command'] == 'sleep' for e in trace), 7)

    def test_ready_engine_keeps_token_unread_until_admission(self):
        result, trace = self.invoke([{'exit': 0, 'output': 'true'}])
        self.assertEqual(result.returncode, 77) # Test install intercepted first effect.
        probes = [e for e in trace if e['command'] == 'runuser']
        self.assertEqual(len(probes), 1)
        self.assertEqual(probes[0]['input_bytes'], 0)
        self.assertEqual(probes[0]['arguments'], ['-u', 'runner-agent', '--', '/usr/bin/env', '-i',
                         'PATH=/usr/bin:/bin', 'HOME=/home/runner-agent', '/usr/bin/podman', '--remote',
                         '--url=unix:///run/anas-podman/podman.sock', 'info', '--format={{.Host.Security.Rootless}}'])
        self.assertEqual(next(e for e in trace if e['command'] == 'install')['input_bytes'], len(TOKEN))
        timeout = next(e for e in trace if e['command'] == 'timeout')
        self.assertEqual(timeout['arguments'][:3], ['--signal=TERM', '--kill-after=1s', '2s'])

    def test_late_readiness_is_retried_without_consuming_token(self):
        result, trace = self.invoke([{'exit': 125}, {'exit': 125}, {'exit': 0, 'output': 'true'}])
        self.assertEqual(result.returncode, 77)
        self.assertEqual(sum(e['command'] == 'runuser' for e in trace), 3)
        self.assertEqual(sum(e['command'] == 'sleep' for e in trace), 2)

    def test_rootful_or_malformed_success_is_not_admitted(self):
        for output in ('false', '', 'true\nprivate', ' true', '{}'):
            with self.subTest(output=output):
                result, trace = self.invoke([{'exit': 0, 'output': output}])
                self.assertEqual(result.returncode, 69)
                self.assertFalse(any(e['command'] == 'install' for e in trace))

    def test_failed_probe_cannot_supply_a_stale_true(self):
        result, trace = self.invoke([{'exit': 125, 'output': 'true'}])
        self.assertEqual(result.returncode, 69)
        self.assertFalse(any(e['command'] == 'install' for e in trace))

    def test_actual_timeout_rejects_true_before_a_hung_probe_exits(self):
        result, trace = self.invoke([{'exit': 0, 'output': 'true', 'sleep': 5}, {'exit': 0, 'output': 'true'}])
        self.assertEqual(result.returncode, 77)
        self.assertEqual(sum(e['command'] == 'runuser' for e in trace), 2)

    def test_active_job_does_not_consume_a_second_token(self):
        result, trace = self.invoke([{'exit': 125}], active=True)
        self.assertEqual(result.returncode, 0)
        self.assertEqual([e['command'] for e in trace], ['systemctl'])

    def test_invalid_arguments_do_not_probe_or_write(self):
        result, trace = self.invoke([{'exit': 0}], arguments=['--unknown'])
        self.assertEqual(result.returncode, 64)
        self.assertEqual(trace, [])


if __name__ == '__main__':
    unittest.main()
